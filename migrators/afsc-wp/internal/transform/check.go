package transform

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"afsc/internal/content"

	"afsc/migrators/afsc-wp/internal/convert"
	"afsc/migrators/afsc-wp/internal/fidelity"
	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/shortcode"
)

// checker validates converted documents with AFSC's validator and renders them with AFSC's renderer, using
// stand-in media ids the way the importer does.
type checker struct {
	s    *state
	ids  map[string]int64
	info map[int64]content.MediaInfo
	keys map[int64]string
}

var wpPostKey = regexp.MustCompile(`:post:(\d+)$`)

func newChecker(s *state, media []*records.Media) *checker {
	c := &checker{s: s, ids: map[string]int64{}, info: map[int64]content.MediaInfo{}, keys: map[int64]string{}}
	for i, m := range media {
		id := int64(i + 1)
		c.ids[m.LegacyKey] = id
		c.keys[id] = m.LegacyKey
		mi := content.MediaInfo{ID: id, Filename: m.Filename, Version: m.SHA256[:8], Mime: m.Mime}
		if m.Width != nil {
			mi.Width, mi.Height = *m.Width, *m.Height
		}
		if sm := wpPostKey.FindStringSubmatch(m.LegacyKey); sm != nil {
			mi.LegacyID, _ = strconv.ParseInt(sm[1], 10, 64)
		}
		c.info[id] = mi
	}
	return c
}

// withIDs returns a copy of doc with media keys replaced by stand-in ids, and any unknown keys.
func (c *checker) withIDs(doc *content.Node) (*content.Node, []string) {
	b, _ := json.Marshal(doc)
	cp, _ := content.Parse(b)
	var unknown []string
	content.Walk(cp, func(n *content.Node) {
		for _, k := range []string{"mediaId", "posterMediaId"} {
			if key, ok := n.Attrs[k].(string); ok {
				if id, ok := c.ids[key]; ok {
					n.Attrs[k] = id
				} else {
					unknown = append(unknown, key)
					n.Attrs[k] = int64(1)
				}
			}
		}
	})
	return cp, unknown
}

func (c *checker) validate(doc *content.Node) []string {
	cp, unknown := c.withIDs(doc)
	var out []string
	for _, k := range unknown {
		out = append(out, "unknown media "+k)
	}
	res := content.Validate(cp, content.Options{AllowedClasses: c.s.allowed, AllowRawHTML: true,
		MediaExists: func(id int64) bool { _, ok := c.info[id]; return ok }})
	for _, e := range res.Errors {
		out = append(out, e.Path+": "+e.Message)
	}
	return out
}

type PostFidelity struct {
	Key   string   `json:"legacy_key"`
	Class string   `json:"class"`
	Diffs []string `json:"diffs,omitempty"`
}

var mediaPathRe = regexp.MustCompile(`/m/(\d+)/`)

// fidelity renders the document with AFSC's renderer and compares it with the WordPress-filtered HTML,
// shortcodes expanded the way WordPress would show them.
func (c *checker) fidelity(key string, postID int64, filtered string, res *convert.Result, rendered map[string]string) PostFidelity {
	cp, _ := c.withIDs(res.Doc)
	rctx := content.RenderContext{PostID: postID, Media: func(id int64) (content.MediaInfo, bool) { mi, ok := c.info[id]; return mi, ok }}
	if c.s.o.Theme != nil {
		rctx.Images = c.s.o.Theme.Images()
	}
	afHTML, _, err := content.Render(cp, rctx)
	if err != nil {
		return PostFidelity{Key: key, Class: fidelity.Lossy, Diffs: []string{"render: " + err.Error()}}
	}
	raw := false
	for _, f := range res.Flags {
		raw = raw || f.Kind == "raw_html" || f.Kind == "shortcode_rendered"
	}
	wp := fidelity.Analyze(c.ExpandShortcodes(filtered, postID, rendered), c.WPSide())
	af := fidelity.Analyze(afHTML, c.AFSCSide())
	class, diffs := fidelity.Compare(wp, af, raw)
	return PostFidelity{Key: key, Class: class, Diffs: diffs}
}

// WPSide reads WordPress markup: wp-image-N first, then the legacy URL index.
func (c *checker) WPSide() fidelity.Side {
	link := func(h string) string {
		if out, ok := c.s.rw.Rewrite(h); ok {
			return out
		}
		return h
	}
	return fidelity.Side{Hosts: c.s.hosts, Link: link, Media: func(u string, cls []string) (string, bool) {
		for _, cl := range cls {
			if n, ok := strings.CutPrefix(cl, "wp-image-"); ok {
				if id, err := strconv.ParseInt(n, 10, 64); err == nil {
					if k, ok := c.s.media.Attachment(id); ok {
						return k, true
					}
				}
			}
		}
		ref, _, ok := c.s.media.Resolve(u)
		return ref.Key, ok
	}}
}

// AFSCSide reads AFSC markup: /m/{id}/ URLs map back through the stand-in ids.
func (c *checker) AFSCSide() fidelity.Side {
	return fidelity.Side{Hosts: c.s.hosts, Media: func(u string, _ []string) (string, bool) {
		if m := mediaPathRe.FindStringSubmatch(u); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			k, ok := c.keys[id]
			return k, ok
		}
		ref, _, ok := c.s.media.Resolve(u)
		return ref.Key, ok
	}}
}

// ExpandShortcodes approximates WordPress's rendering of converted shortcodes, as the comparison reference for
// posts the baseline crawl doesn't cover.
func (c *checker) ExpandShortcodes(src string, postID int64, rendered map[string]string) string {
	tags := map[string]bool{}
	for _, t := range c.s.shortcodeTags() {
		tags[t] = true
	}
	var b strings.Builder
	for _, seg := range shortcode.Parse(src, tags) {
		if seg.SC == nil {
			b.WriteString(seg.Text)
			continue
		}
		sc := seg.SC
		switch c.s.decide("shortcode", sc.Tag, "keep_text") {
		case "render_php":
			b.WriteString(rendered[sc.Tag+"|"+phpID(strconv.FormatInt(postID, 10), sc.Raw)])
			continue
		case "drop":
			b.WriteString(c.ExpandShortcodes(sc.Content, postID, rendered))
			continue
		case "keep_text":
			b.WriteString(sc.Raw)
			continue
		}
		switch sc.Tag {
		case "caption", "wp_caption":
			b.WriteString("<div>" + c.ExpandShortcodes(sc.Content, postID, rendered))
			if cap := sc.Attrs["caption"]; cap != "" {
				b.WriteString(" " + cap)
			}
			b.WriteString("</div>")
		case "gallery":
			b.WriteString("<div>")
			for _, id := range convert.GalleryIDs(sc, postID, c.s.media) {
				k, ok := c.s.media.Attachment(id)
				if !ok {
					continue
				}
				img := fmt.Sprintf(`<img class="wp-image-%d" src="">`, id)
				if sc.Attrs["link"] != "none" {
					img = `<a href="` + html.EscapeString(c.s.media.OrigPath(k)) + `">` + img + `</a>`
				}
				b.WriteString(img)
			}
			b.WriteString("</div>")
		case "embed":
			b.WriteString(`<iframe src="` + html.EscapeString(strings.TrimSpace(html.UnescapeString(sc.Content))) + `"></iframe>`)
		case "youtube", "vimeo", "tweet", "instagram", "spotify", "soundcloud", "googlemaps":
			u := sc.First()
			for _, k := range []string{"url", "src", "id"} {
				if u == "" {
					u = sc.Attrs[k]
				}
			}
			if e, ok := convert.EmbedFromURL(jetpackURL(sc.Tag, html.UnescapeString(u))); ok {
				b.WriteString(`<iframe src="` + html.EscapeString(e.Attrs["url"].(string)) + `"></iframe>`)
			}
		}
	}
	return b.String()
}

var digits = regexp.MustCompile(`^\d+$`)

func jetpackURL(tag, u string) string {
	switch {
	case tag == "vimeo" && digits.MatchString(u):
		return "https://vimeo.com/" + u
	case tag == "tweet" && digits.MatchString(u):
		return "https://x.com/i/status/" + u
	case tag == "youtube" && !strings.Contains(u, "."):
		return "https://www.youtube.com/watch?v=" + u
	}
	return u
}
