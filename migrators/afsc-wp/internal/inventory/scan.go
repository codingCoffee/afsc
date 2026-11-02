// Package inventory scans the extract, uploads and site code (migration plan §3), writes reports/inventory.*
// and proposes decisions.json for a human to confirm.
package inventory

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"afsc/migrators/afsc-wp/internal/blocks"
	"afsc/migrators/afsc-wp/internal/convert"
	"afsc/migrators/afsc-wp/internal/mediaidx"
	"afsc/migrators/afsc-wp/internal/shortcode"
)

type ShortcodeStat struct {
	Tag       string   `json:"tag"`
	Count     int      `json:"count"`
	Enclosing int      `json:"enclosing"`
	Attrs     []string `json:"attrs"`
	Posts     []string `json:"posts"`
	Origin    string   `json:"origin"` // core, jetpack, site-code, unknown
}

type Counter map[string]int

func (c Counter) add(k string) { c[k]++ }

type Scan struct {
	Shortcodes   map[string]*ShortcodeStat `json:"shortcodes"`
	InTagCodes   int                       `json:"shortcodes_in_html_attributes"`
	Blocks       Counter                   `json:"blocks"`
	BlockPosts   map[string][]string       `json:"-"`
	Kinds        Counter                   `json:"content_kinds"`
	Elements     Counter                   `json:"elements"`
	Attributes   Counter                   `json:"attributes"`
	Classes      Counter                   `json:"classes"`
	Styles       Counter                   `json:"style_properties"`
	DataAttrs    Counter                   `json:"data_attributes"`
	EmbedHosts   Counter                   `json:"iframe_script_object_hosts"`
	EmbedPosts   map[string][]string       `json:"-"`
	ElementPosts map[string][]string       `json:"-"`
	ImageForms   Counter                   `json:"image_forms"`
	ExternalImg  Counter                   `json:"external_image_hosts"`
	MissingImg   map[string][]string       `json:"missing_images"`
	Links        Counter                   `json:"links"`
	ExternalLink Counter                   `json:"external_link_hosts"`
	OEmbedLines  Counter                   `json:"oembed_candidate_hosts"`
	OEmbedPosts  map[string][]string       `json:"-"`
	Markers      Counter                   `json:"markers"`
	MarkerPosts  map[string][]string       `json:"-"`
	OEmbedCache  Counter                   `json:"oembed_cache"`
}

func newScan() *Scan {
	return &Scan{Shortcodes: map[string]*ShortcodeStat{}, Blocks: Counter{}, BlockPosts: map[string][]string{}, Kinds: Counter{},
		Elements: Counter{}, Attributes: Counter{}, Classes: Counter{}, Styles: Counter{}, DataAttrs: Counter{},
		EmbedHosts: Counter{}, EmbedPosts: map[string][]string{}, ElementPosts: map[string][]string{}, ImageForms: Counter{},
		ExternalImg: Counter{}, MissingImg: map[string][]string{}, Links: Counter{}, ExternalLink: Counter{},
		OEmbedLines: Counter{}, OEmbedPosts: map[string][]string{}, Markers: Counter{}, MarkerPosts: map[string][]string{},
		OEmbedCache: Counter{}}
}

func addSample(m map[string][]string, k, key string) {
	if l := m[k]; len(l) < 10 && !slices.Contains(l, key) {
		m[k] = append(l, key)
	}
}

var (
	bareURLLine = regexp.MustCompile(`(?m)^\s*(https?://[^\s<>"]+)\s*$`)
	nbspRun     = regexp.MustCompile(`(&nbsp;|\x{00a0}){2,}`)
	emptyPara   = regexp.MustCompile(`<p>(\s|&nbsp;|\x{00a0})*</p>`)
	smiley      = regexp.MustCompile(`(^|\s)(:\)|:-\)|:D|:-D|;\)|;-\)|:\(|:-\(|:P|:-P)(\s|$)`)
	htmlComment = regexp.MustCompile(`<!--(.*?)-->`)
)

var dangerous = map[string]bool{"script": true, "style": true, "form": true, "object": true, "embed": true, "noscript": true,
	"input": true, "select": true, "textarea": true, "button": true, "canvas": true, "svg": true, "math": true, "audio": true,
	"applet": true, "marquee": true, "template": true}

// content scans one post's raw content.
func (s *Scan) content(key, raw string, media *mediaidx.Index, hosts map[string]bool) {
	s.Kinds.add(convert.Kind(raw))
	for _, o := range shortcode.Scan(raw) {
		st := s.Shortcodes[o.Tag]
		if st == nil {
			st = &ShortcodeStat{Tag: o.Tag}
			s.Shortcodes[o.Tag] = st
		}
		st.Count++
		if o.Enclosing {
			st.Enclosing++
		}
		for _, a := range o.AttrNames {
			if !slices.Contains(st.Attrs, a) {
				st.Attrs = append(st.Attrs, a)
			}
		}
		if len(st.Posts) < 10 && !slices.Contains(st.Posts, key) {
			st.Posts = append(st.Posts, key)
		}
	}
	s.InTagCodes += shortcode.InTagCount(raw)
	for name, n := range blocks.Names(raw) {
		s.Blocks[name] += n
		addSample(s.BlockPosts, name, key)
	}
	marker := func(name string, n int) {
		if n > 0 {
			s.Markers[name] += n
			addSample(s.MarkerPosts, name, key)
		}
	}
	marker("more", strings.Count(raw, "<!--more"))
	marker("nextpage", strings.Count(raw, "<!--nextpage-->"))
	marker("nbsp_runs", len(nbspRun.FindAllString(raw, -1)))
	marker("empty_paragraphs", len(emptyPara.FindAllString(raw, -1))+strings.Count(raw, "\n&nbsp;\n"))
	marker("smilies", len(smiley.FindAllString(raw, -1)))
	for _, m := range htmlComment.FindAllStringSubmatch(raw, -1) {
		c := strings.TrimSpace(m[1])
		if !strings.HasPrefix(c, "more") && c != "nextpage" && !strings.HasPrefix(c, "wp:") && !strings.HasPrefix(c, "/wp:") {
			marker("html_comments", 1)
		}
	}
	for _, m := range bareURLLine.FindAllStringSubmatch(raw, -1) {
		h := host(m[1])
		s.OEmbedLines.add(h)
		addSample(s.OEmbedPosts, h, key)
	}
	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			s.element(key, n, media, hosts)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func host(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(u, "https:"), "http:"), "//")
	h, _, _ := strings.Cut(u, "/")
	h, _, _ = strings.Cut(h, "?")
	return strings.ToLower(h)
}

func (s *Scan) element(key string, n *html.Node, media *mediaidx.Index, hosts map[string]bool) {
	s.Elements.add(n.Data)
	if dangerous[n.Data] {
		addSample(s.ElementPosts, n.Data, key)
	}
	for _, a := range n.Attr {
		switch {
		case strings.HasPrefix(a.Key, "data-"):
			s.DataAttrs.add(a.Key)
		case a.Key == "class":
			for _, c := range strings.Fields(a.Val) {
				s.Classes.add(c)
			}
		case a.Key == "style":
			for _, d := range strings.Split(a.Val, ";") {
				if p, _, ok := strings.Cut(d, ":"); ok {
					s.Styles.add(strings.ToLower(strings.TrimSpace(p)))
				}
			}
		}
		s.Attributes.add(n.Data + "@" + a.Key)
	}
	switch n.Data {
	case "iframe", "script", "object", "embed":
		src := attr(n, "src")
		if src == "" {
			src = attr(n, "data")
		}
		if src != "" {
			h := host(src)
			s.EmbedHosts.add(n.Data + " " + h)
			addSample(s.EmbedPosts, h, key)
		}
	case "img":
		src := attr(n, "src")
		switch {
		case strings.HasPrefix(src, "data:"):
			s.ImageForms.add("data URI")
		case media.IsUpload(src):
			ref, rel, ok := media.Resolve(src)
			form := "uploads"
			if strings.Contains(src, ".wp.com/") {
				form = "jetpack cdn"
			}
			if strings.Contains(rel, "-scaled.") {
				form += " -scaled"
			} else if ref.Size != "" {
				form += " size variant"
			}
			if !ok {
				form += " (missing)"
				addSample(s.MissingImg, rel, key)
			}
			s.ImageForms.add(form)
		default:
			s.ImageForms.add("external")
			s.ExternalImg.add(host(src))
		}
	case "a":
		h := attr(n, "href")
		lh := strings.ToLower(h)
		switch {
		case strings.HasPrefix(h, "#"):
			s.Links.add("anchor (#)")
		case attr(n, "name") != "" && h == "":
			s.Links.add("named anchor")
		case strings.Contains(lh, "?p=") || strings.Contains(lh, "?page_id=") || strings.Contains(lh, "&p="):
			s.Links.add("?p= style")
		case strings.Contains(lh, "attachment_id=") || strings.Contains(lh, "/attachment/"):
			s.Links.add("attachment page")
		case media.IsUpload(h):
			s.Links.add("uploads file")
		case strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//"):
			s.Links.add("internal (relative)")
		case hosts[host(h)]:
			s.Links.add("internal (absolute)")
		case strings.HasPrefix(lh, "http"):
			s.Links.add("external")
			s.ExternalLink.add(host(h))
		case h != "":
			s.Links.add("other scheme or relative")
		}
	}
}

type Hook struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

var hookRes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"filter", regexp.MustCompile(`add_filter\s*\(\s*['"](the_content|the_excerpt|the_title|wp_head|body_class|post_class)['"]\s*,\s*([^,\)]+)`)},
	{"action", regexp.MustCompile(`add_action\s*\(\s*['"](wp_head|wp_footer|wp_body_open|wp_enqueue_scripts)['"]\s*,\s*([^,\)]+)`)},
	{"shortcode", regexp.MustCompile(`add_shortcode\s*\(\s*['"]([^'"]+)['"]\s*,\s*([^,\)]+)`)},
}

// Hooks finds content filters, head/footer actions and shortcode registrations in site PHP code.
func Hooks(dirs []string) ([]Hook, error) {
	var out []Hook
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".php") {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			rel, _ := filepath.Rel(dir, p)
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1<<20), 16<<20)
			line := 0
			for sc.Scan() {
				line++
				for _, h := range hookRes {
					for _, m := range h.re.FindAllStringSubmatch(sc.Text(), -1) {
						name := m[1]
						if h.kind != "shortcode" {
							name = m[1] + ":" + strings.Trim(strings.TrimSpace(m[2]), `'"`)
						}
						out = append(out, Hook{Kind: h.kind, Name: name, File: filepath.Base(dir) + "/" + filepath.ToSlash(rel), Line: line})
					}
				}
			}
			return sc.Err()
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
