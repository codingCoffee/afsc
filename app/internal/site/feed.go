package site

import (
  "bytes"
  "encoding/xml"
  "html"
  "net/http"
  "slices"
  "strings"
  "time"

  "afsc/internal/theme"
)

const excerptWords = 55

// feedExcerpt follows the excerpt func: explicit excerpt, else text before readMore, else the first 55 words.
func feedExcerpt(p *theme.Post) string {
  switch {
  case p.Excerpt != "":
    return p.Excerpt
  case p.HasMore:
    return p.TeaserText
  }
  return theme.TrimWords(p.ContentText, excerptWords, " […]")
}

func xmlText(s string) string {
  var b strings.Builder
  _ = xml.EscapeText(&b, []byte(s))
  return b.String()
}

// cdata wraps s in CDATA, splitting any "]]>" and dropping characters XML 1.0 forbids.
func cdata(s string) string {
  s = strings.Map(func(r rune) rune {
    if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || r >= 0x10000 {
      return r
    }
    return -1
  }, s)
  return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
}

func rssDate(t time.Time) string { return t.UTC().Format(time.RFC1123Z) }

// serveFeed writes an RSS 2.0 feed compatible with WordPress output (§6.4).
func (st *Site) serveFeed(w http.ResponseWriter, r *http.Request, snap *snapshot, res Result) error {
  s := snap.settings
  title, link, f := s.Site.Title, snap.abs("/"), filter{}
  switch res.FeedOf {
  case "category", "tag":
    t := snap.termView(res.ID)
    title, link, f.termIDs = title+" » "+t.Name, t.URL, []int64{res.ID}
    if res.FeedOf == "category" {
      f.termIDs = snap.termDescendants(res.ID)
    }
  case "author":
    a := snap.authorView(res.ID)
    title, link, f.authorID = title+" » "+a.Name, a.URL, res.ID
  }
  rows, _, err := st.listPosts(r.Context(), f, s.Reading.FeedItems, 0)
  if err != nil {
    return err
  }
  if err := st.hydrate(r.Context(), snap, rows, hydrateOpts{teaser: true, feed: !s.Reading.FeedExcerptOnly}); err != nil {
    return err
  }

  desc := s.Site.Tagline
  if desc == "" {
    desc = s.Site.Title
  }
  var b bytes.Buffer
  b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
  b.WriteString(`<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"` +
    ` xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:atom="http://www.w3.org/2005/Atom">` + "\n<channel>\n")
  b.WriteString("<title>" + xmlText(title) + "</title>\n")
  b.WriteString(`<atom:link href="` + xmlText(snap.abs(r.URL.Path)) + `" rel="self" type="application/rss+xml"/>` + "\n")
  b.WriteString("<link>" + xmlText(link) + "</link>\n")
  b.WriteString("<description>" + xmlText(desc) + "</description>\n")
  var latest time.Time
  for _, p := range rows {
    if p.view.UpdatedAt.After(latest) {
      latest = p.view.UpdatedAt
    }
  }
  if !latest.IsZero() {
    b.WriteString("<lastBuildDate>" + rssDate(latest) + "</lastBuildDate>\n")
  }
  b.WriteString("<language>" + xmlText(s.Site.Language) + "</language>\n")

  for _, row := range rows {
    p := row.view
    b.WriteString("<item>\n<title>" + xmlText(p.Title) + "</title>\n<link>" + xmlText(p.URL) + "</link>\n")
    for _, a := range p.Authors {
      b.WriteString("<dc:creator>" + cdata(a.Name) + "</dc:creator>\n")
    }
    b.WriteString("<pubDate>" + rssDate(p.PublishedAt) + "</pubDate>\n")
    for _, t := range slices.Concat(p.Categories, p.Tags) {
      b.WriteString("<category>" + cdata(t.Name) + "</category>\n")
    }
    if p.FeedGUID != "" {
      b.WriteString(`<guid isPermaLink="false">` + xmlText(p.FeedGUID) + "</guid>\n")
    } else {
      b.WriteString(`<guid isPermaLink="true">` + xmlText(p.URL) + "</guid>\n")
    }
    excerpt := feedExcerpt(p)
    b.WriteString("<description>" + cdata(excerpt) + "</description>\n")
    body := string(p.ContentHTML)
    if s.Reading.FeedExcerptOnly {
      body = "<p>" + html.EscapeString(excerpt) + "</p>"
    }
    b.WriteString("<content:encoded>" + cdata(body) + "</content:encoded>\n</item>\n")
  }
  b.WriteString("</channel>\n</rss>\n")

  w.Header().Set("Content-Type", "application/rss+xml; charset=UTF-8")
  w.WriteHeader(http.StatusOK)
  _, _ = b.WriteTo(w)
  return nil
}
