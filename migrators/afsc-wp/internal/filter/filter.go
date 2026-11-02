// Package filter runs WordPress's own content filters through tools/wp-filter.php (migration plan §6.2), or
// replays a recorded NDJSON file of its output. wpautop and wptexturize are never reimplemented in Go.
package filter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"afsc/migrators/afsc-wp/internal/extract"
)

type Item struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type Result struct {
	ID    string `json:"id"`
	HTML  string `json:"html"`
	Error string `json:"error,omitempty"`
}

// Request is one batch: plain filtering (Tag empty) or rendering one shortcode with PHP.
type Request struct {
	Tag        string   // render mode: the only shortcode registered
	Includes   []string // render mode: PHP files that register Tag
	Shortcodes []string // filter mode: tags registered as no-ops, so wptexturize and shortcode_unautop see them
	Items      []Item
}

type Runner interface {
	Run(ctx context.Context, req Request) (map[string]string, error)
}

// Command runs filter.command with mode flags appended, writing NDJSON items to stdin.
type Command struct {
	Argv    []string
	Timeout time.Duration
	Batch   int
}

func (c *Command) Run(ctx context.Context, req Request) (map[string]string, error) {
	if len(c.Argv) == 0 {
		return nil, fmt.Errorf("filter.command is empty; set it or filter.replay_file in afsc-wp.json")
	}
	out := map[string]string{}
	batch := c.Batch
	if batch <= 0 {
		batch = 500
	}
	for start := 0; start < len(req.Items); start += batch {
		end := min(start+batch, len(req.Items))
		part := req
		part.Items = req.Items[start:end]
		got, err := c.run(ctx, part)
		if err != nil {
			return nil, err
		}
		for k, v := range got {
			out[k] = v
		}
	}
	return out, nil
}

func (c *Command) args(req Request) []string {
	args := append([]string{}, c.Argv[1:]...)
	if req.Tag != "" {
		args = append(args, "--render-shortcode="+req.Tag)
		if len(req.Includes) > 0 {
			args = append(args, "--include="+strings.Join(req.Includes, ","))
		}
	} else if len(req.Shortcodes) > 0 {
		args = append(args, "--shortcodes="+strings.Join(req.Shortcodes, ","))
	}
	return args
}

func (c *Command) run(ctx context.Context, req Request) (map[string]string, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	enc.SetEscapeHTML(false)
	for _, it := range req.Items {
		if err := enc.Encode(it); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, c.Argv[0], c.args(req)...)
	cmd.Stdin = &in
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("filter command failed: %w: %s", err, tail(stderr.String()))
	}
	return decode(&stdout, req.Items)
}

func tail(s string) string {
	if len(s) > 2000 {
		return "…" + s[len(s)-2000:]
	}
	return s
}

func decode(r *bytes.Buffer, items []Item) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var res Result
		if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
			return nil, fmt.Errorf("filter output: %w", err)
		}
		if res.Error != "" {
			return nil, fmt.Errorf("filter failed on %s: %s", res.ID, res.Error)
		}
		out[res.ID] = res.HTML
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var missing []string
	for _, it := range items {
		if _, ok := out[it.ID]; !ok {
			missing = append(missing, it.ID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("filter output is missing %d items: %s", len(missing), strings.Join(first(missing, 20), ", "))
	}
	return out, nil
}

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ReplayRecord is one line of a recorded wp-filter.php run. Tag is set for render-shortcode output.
type ReplayRecord struct {
	Tag  string `json:"tag,omitempty"`
	ID   string `json:"id"`
	HTML string `json:"html"`
}

// Replay answers from a recorded NDJSON file instead of running PHP.
type Replay struct {
	Path string
	recs map[string]string
}

func (r *Replay) Run(_ context.Context, req Request) (map[string]string, error) {
	if r.recs == nil {
		recs, err := extract.ReadNDJSON[ReplayRecord](r.Path)
		if err != nil {
			return nil, err
		}
		if recs == nil {
			return nil, fmt.Errorf("replay file %s is empty or missing", r.Path)
		}
		r.recs = map[string]string{}
		for _, rec := range recs {
			r.recs[rec.Tag+"|"+rec.ID] = rec.HTML
		}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, it := range req.Items {
		if h, ok := r.recs[req.Tag+"|"+it.ID]; ok {
			enc.Encode(Result{ID: it.ID, HTML: h})
		}
	}
	return decode(&b, req.Items)
}

// Cached wraps a runner with an on-disk cache keyed by request mode and content hash.
type Cached struct {
	Next Runner
	Dir  string
}

type cacheRec struct {
	Key  string `json:"key"`
	HTML string `json:"html"`
}

func cacheKey(req Request, it Item) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s", req.Tag, strings.Join(req.Includes, ","), strings.Join(req.Shortcodes, ","), it.ID, it.Content)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Cached) file(req Request) string {
	name := "filtered.ndjson"
	if req.Tag != "" {
		name = "shortcode-" + req.Tag + ".ndjson"
	}
	return filepath.Join(c.Dir, name)
}

func (c *Cached) Run(ctx context.Context, req Request) (map[string]string, error) {
	recs, err := extract.ReadNDJSON[cacheRec](c.file(req))
	if err != nil {
		return nil, err
	}
	cache := map[string]string{}
	for _, r := range recs {
		cache[r.Key] = r.HTML
	}
	out := map[string]string{}
	var todo []Item
	for _, it := range req.Items {
		if h, ok := cache[cacheKey(req, it)]; ok {
			out[it.ID] = h
		} else {
			todo = append(todo, it)
		}
	}
	if len(todo) > 0 {
		sub := req
		sub.Items = todo
		got, err := c.Next.Run(ctx, sub)
		if err != nil {
			return nil, err
		}
		for k, v := range got {
			out[k] = v
		}
	}
	keep := make([]cacheRec, 0, len(req.Items))
	for _, it := range req.Items {
		keep = append(keep, cacheRec{Key: cacheKey(req, it), HTML: out[it.ID]})
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].Key < keep[j].Key })
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return nil, err
	}
	return out, extract.WriteNDJSON(c.file(req), keep)
}

// Recorder passes requests through and keeps every result as replay records (for regenerating golden files).
type Recorder struct {
	Next    Runner
	Records []ReplayRecord
}

func (r *Recorder) Run(ctx context.Context, req Request) (map[string]string, error) {
	out, err := r.Next.Run(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, it := range req.Items {
		r.Records = append(r.Records, ReplayRecord{Tag: req.Tag, ID: it.ID, HTML: out[it.ID]})
	}
	sort.Slice(r.Records, func(i, j int) bool {
		if r.Records[i].Tag != r.Records[j].Tag {
			return r.Records[i].Tag < r.Records[j].Tag
		}
		return r.Records[i].ID < r.Records[j].ID
	})
	return out, nil
}
