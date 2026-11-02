package transform

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/records"
)

// Meta is work/transform/meta.json.
type Meta struct {
	GeneratedAt time.Time `json:"generated_at"`
	Source      string    `json:"source"`
	Theme       string    `json:"theme,omitempty"`
}

// MediaSource maps a media record to the uploads-relative file bundle copies into files/.
type MediaSource struct {
	Key  string `json:"legacy_key"`
	Path string `json:"path"`
}

// LinkRewrite is one href the conversion changed (work/link-map.ndjson).
type LinkRewrite struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type FilteredPost struct {
	ID   int64  `json:"id"`
	HTML string `json:"html"`
}

// Save writes the records under work/transform, the URL map under work/, and the reports.
func (o *Output) Save(workDir, reportsDir, source, theme string) error {
	dir := filepath.Join(workDir, "transform")
	var sources []MediaSource
	for _, m := range o.Media {
		sources = append(sources, MediaSource{Key: m.LegacyKey, Path: m.SourcePath})
	}
	var fp []FilteredPost
	for _, k := range sortedKeys(o.Filtered) {
		id, _ := strconv.ParseInt(k, 10, 64)
		fp = append(fp, FilteredPost{ID: id, HTML: o.Filtered[k]})
	}
	sort.Slice(fp, func(i, j int) bool { return fp[i].ID < fp[j].ID })
	steps := []func() error{
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "users.ndjson"), o.Users) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "authors.ndjson"), o.Authors) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "media.ndjson"), o.Media) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "media-sources.ndjson"), sources) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "terms.ndjson"), o.Terms) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "posts.ndjson"), o.Posts) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "comments.ndjson"), o.Comments) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "menus.ndjson"), o.Menus) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "redirects.ndjson"), o.Redirects) },
		func() error { return extract.WriteNDJSON(filepath.Join(dir, "legacy.ndjson"), o.Legacy) },
		func() error { return os.WriteFile(filepath.Join(dir, "settings.json"), o.Settings, 0o600) },
		func() error {
			return extract.WriteJSON(filepath.Join(dir, "meta.json"), Meta{GeneratedAt: o.GeneratedAt, Source: source, Theme: theme})
		},
		func() error { return extract.WriteNDJSON(filepath.Join(workDir, "url-map.ndjson"), o.URLMap) },
		func() error { return extract.WriteNDJSON(filepath.Join(workDir, "filtered", "posts.ndjson"), fp) },
		func() error { return extract.WriteNDJSON(filepath.Join(workDir, "paths.ndjson"), o.Paths) },
		func() error {
			var lm []LinkRewrite
			for _, k := range sortedKeys(o.LinkMap) {
				lm = append(lm, LinkRewrite{From: k, To: o.LinkMap[k]})
			}
			return extract.WriteNDJSON(filepath.Join(workDir, "link-map.ndjson"), lm)
		},
		func() error {
			var ref []FilteredPost
			for _, k := range sortedKeys(o.Reference) {
				id, _ := strconv.ParseInt(k, 10, 64)
				ref = append(ref, FilteredPost{ID: id, HTML: o.Reference[k]})
			}
			sort.Slice(ref, func(i, j int) bool { return ref[i].ID < ref[j].ID })
			return extract.WriteNDJSON(filepath.Join(workDir, "filtered", "reference.ndjson"), ref)
		},
		func() error { return extract.WriteJSON(filepath.Join(reportsDir, "transform.json"), o.Report) },
		func() error { return o.Report.Markdown().Write(filepath.Join(reportsDir, "transform.md")) },
		func() error {
			return o.Report.FidelityMarkdown().Write(filepath.Join(reportsDir, "content-fidelity-pre.md"))
		},
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	return nil
}

// Loaded is a transform read back from work/transform, for the bundle step.
type Loaded struct {
	Meta      Meta
	Users     []*records.User
	Authors   []*records.Author
	Media     []*records.Media
	Sources   map[string]string
	Terms     []*records.Term
	Posts     []*records.Post
	Comments  []*records.Comment
	Menus     []*records.Menu
	Redirects []*records.Redirect
	Legacy    []*records.Legacy
	Settings  []byte
}

func Load(workDir string) (*Loaded, error) {
	dir := filepath.Join(workDir, "transform")
	l := &Loaded{Sources: map[string]string{}}
	if err := extract.ReadJSON(filepath.Join(dir, "meta.json"), &l.Meta); err != nil {
		return nil, err
	}
	var err error
	read := func(name string, into func() error) {
		if err == nil {
			err = into()
		}
	}
	read("users", func() (e error) {
		l.Users, e = extract.ReadNDJSON[*records.User](filepath.Join(dir, "users.ndjson"))
		return
	})
	read("authors", func() (e error) {
		l.Authors, e = extract.ReadNDJSON[*records.Author](filepath.Join(dir, "authors.ndjson"))
		return
	})
	read("media", func() (e error) {
		l.Media, e = extract.ReadNDJSON[*records.Media](filepath.Join(dir, "media.ndjson"))
		return
	})
	read("terms", func() (e error) {
		l.Terms, e = extract.ReadNDJSON[*records.Term](filepath.Join(dir, "terms.ndjson"))
		return
	})
	read("posts", func() (e error) {
		l.Posts, e = extract.ReadNDJSON[*records.Post](filepath.Join(dir, "posts.ndjson"))
		return
	})
	read("comments", func() (e error) {
		l.Comments, e = extract.ReadNDJSON[*records.Comment](filepath.Join(dir, "comments.ndjson"))
		return
	})
	read("menus", func() (e error) {
		l.Menus, e = extract.ReadNDJSON[*records.Menu](filepath.Join(dir, "menus.ndjson"))
		return
	})
	read("redirects", func() (e error) {
		l.Redirects, e = extract.ReadNDJSON[*records.Redirect](filepath.Join(dir, "redirects.ndjson"))
		return
	})
	read("legacy", func() (e error) {
		l.Legacy, e = extract.ReadNDJSON[*records.Legacy](filepath.Join(dir, "legacy.ndjson"))
		return
	})
	read("sources", func() error {
		srcs, e := extract.ReadNDJSON[MediaSource](filepath.Join(dir, "media-sources.ndjson"))
		for _, s := range srcs {
			l.Sources[s.Key] = s.Path
		}
		return e
	})
	read("settings", func() (e error) { l.Settings, e = os.ReadFile(filepath.Join(dir, "settings.json")); return })
	if err != nil {
		return nil, err
	}
	return l, nil
}
