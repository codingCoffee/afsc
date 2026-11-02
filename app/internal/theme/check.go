package theme

import (
	"errors"
	"io"

	"afsc/internal/content"
)

type CheckResult struct {
	Template string
	Fixture  string
	Err      error
}

// Check loads the theme in dir and renders every template and node partial against fixture data.
// prepare, when set, fills derived fields such as PageData.Head before each render.
func Check(dir string, prepare func(*Theme, *PageData), extra ...Options) ([]CheckResult, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	var opts Options
	if len(extra) > 0 {
		opts = extra[0]
	}
	opts.Dev, opts.OnReload, opts.Backend = false, nil, FixtureBackend(m)
	t, err := Load(dir, opts)
	if err != nil {
		return nil, err
	}
	st, _ := t.current()

	var out []CheckResult
	for _, name := range t.Templates() {
		kinds := FixtureKinds(name)
		if kinds == nil {
			out = append(out, CheckResult{Template: name, Err: errors.New("not part of the template hierarchy; shared markup belongs in parts/")})
			continue
		}
		for _, kind := range kinds {
			for _, full := range []bool{true, false} {
				label := kind + " (sparse)"
				if full {
					label = kind + " (full)"
				}
				d := Fixture(kind, full)
				if prepare != nil {
					prepare(t, d)
				}
				err := st.exec(io.Discard, st.pages[name], name, d)
				out = append(out, CheckResult{Template: name, Fixture: label, Err: err})
			}
		}
	}

	doc := content.SampleDoc()
	for _, node := range t.Nodes() {
		var target *content.Node
		content.Walk(doc, func(n *content.Node) {
			if target == nil && n.Type == node {
				target = n
			}
		})
		r := CheckResult{Template: "nodes/" + node + ".html", Fixture: "sample " + node}
		if target == nil {
			r.Err = errors.New("no fixture content for this node")
		} else {
			_, _, r.Err = content.Render(target, content.RenderContext{PostID: 123, Media: content.SampleMedia, Partials: t,
				Images: st.manifest.Images()})
		}
		out = append(out, r)
	}
	return out, nil
}
