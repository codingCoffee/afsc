package filter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type countRunner struct {
	calls int
	items int
}

func (c *countRunner) Run(_ context.Context, req Request) (map[string]string, error) {
	c.calls++
	c.items += len(req.Items)
	out := map[string]string{}
	for _, it := range req.Items {
		out[it.ID] = "<p>" + it.Content + "</p>"
	}
	return out, nil
}

func TestReplay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "golden.ndjson")
	golden := `{"id":"1","html":"<p>One</p>\n"}` + "\n" + `{"tag":"pull","id":"1#0","html":"<blockquote>Q</blockquote>"}` + "\n"
	if err := os.WriteFile(p, []byte(golden), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Replay{Path: p}
	got, err := r.Run(context.Background(), Request{Items: []Item{{ID: "1", Content: "One"}}})
	if err != nil || got["1"] != "<p>One</p>\n" {
		t.Fatalf("filter replay: %v %v", got, err)
	}
	got, err = r.Run(context.Background(), Request{Tag: "pull", Items: []Item{{ID: "1#0"}}})
	if err != nil || got["1#0"] != "<blockquote>Q</blockquote>" {
		t.Fatalf("shortcode replay: %v %v", got, err)
	}
	if _, err := r.Run(context.Background(), Request{Items: []Item{{ID: "2"}}}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("want missing error, got %v", err)
	}
}

func TestCached(t *testing.T) {
	inner := &countRunner{}
	c := &Cached{Next: inner, Dir: t.TempDir()}
	req := Request{Shortcodes: []string{"gallery"}, Items: []Item{{ID: "1", Content: "a"}, {ID: "2", Content: "b"}}}
	if _, err := c.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Items[1].Content = "changed"
	got, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 || inner.items != 3 || got["2"] != "<p>changed</p>" {
		t.Fatalf("calls=%d items=%d got=%v", inner.calls, inner.items, got)
	}
}

func TestCommandArgs(t *testing.T) {
	c := &Command{Argv: []string{"docker", "run", "img", "php", "/tools/wp-filter.php"}}
	got := strings.Join(c.args(Request{Shortcodes: []string{"caption", "gallery"}}), " ")
	if got != "run img php /tools/wp-filter.php --shortcodes=caption,gallery" {
		t.Fatalf("filter args: %s", got)
	}
	got = strings.Join(c.args(Request{Tag: "pull", Includes: []string{"/wp/a.php"}}), " ")
	if !strings.HasSuffix(got, "--render-shortcode=pull --include=/wp/a.php") {
		t.Fatalf("render args: %s", got)
	}
}
