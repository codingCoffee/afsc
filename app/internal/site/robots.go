package site

import (
	"context"
	"net/http"
	"strings"
)

const (
	kindText   = "text" // robots.txt, llms.txt and the IndexNow key file
	robotsPath = "/robots.txt"
	llmsPath   = "/llms.txt"
)

// renderText serves robots.txt, llms.txt or the IndexNow key file.
func (st *Site) renderText(ctx context.Context, snap *snapshot, res Result) (*response, error) {
	s := snap.settings.SEO
	var body string
	switch res.File {
	case robotsPath:
		body = s.RobotsTxt
		if body == "" {
			body = "User-agent: *\nDisallow:\n\nSitemap: " + snap.abs(sitemapIndexPath) + "\n"
		}
	case llmsPath:
		body = s.LLMsTxt.Body
		if s.LLMsTxt.Mode == "auto" {
			var err error
			if body, err = st.llmsTxt(ctx, snap); err != nil {
				return nil, err
			}
		}
	default:
		body = s.IndexNow.Key
	}
	return &response{status: http.StatusOK, header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		body: []byte(body)}, nil
}

var mdLinkText = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`)

// llmsTxt follows llmstxt.org: the site name, the tagline as a blockquote, then top-level pages and categories.
// The lists use the sitemap entries, so noindex, canonicalized-away and empty archives stay out.
func (st *Site) llmsTxt(ctx context.Context, snap *snapshot) (string, error) {
	pages, err := st.postEntries(ctx, snap, "page")
	if err != nil {
		return "", err
	}
	cats, err := st.termEntries(ctx, snap, "category")
	if err != nil {
		return "", err
	}
	line := func(name, url, desc string) string {
		l := "- [" + mdLinkText.Replace(oneLine(name)) + "](" + url + ")"
		if desc = oneLine(desc); desc != "" {
			l += ": " + desc
		}
		return l + "\n"
	}
	var pageLines, catLines []string
	for _, e := range pages {
		if p := snap.pages[e.id]; p != nil && p.ParentID == 0 {
			pageLines = append(pageLines, line(p.Title, e.loc, ""))
		}
	}
	for _, e := range cats {
		if t := snap.terms[e.id]; t != nil && t.ParentID == 0 {
			catLines = append(catLines, line(t.Name, e.loc, t.Description))
		}
	}

	site := snap.settings.Site
	var b strings.Builder
	b.WriteString("# " + oneLine(site.Title) + "\n")
	if tag := oneLine(site.Tagline); tag != "" {
		b.WriteString("\n> " + tag + "\n")
	}
	for _, sec := range []struct {
		title string
		lines []string
	}{{"Pages", pageLines}, {"Categories", catLines}} {
		if len(sec.lines) > 0 {
			b.WriteString("\n## " + sec.title + "\n\n" + strings.Join(sec.lines, ""))
		}
	}
	return b.String(), nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
