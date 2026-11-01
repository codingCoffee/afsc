// Package sanitize implements the allowlist HTML sanitizer policies from §7.5 of the build plan.
package sanitize

import (
  "html"
  "regexp"
  "strings"
  "unicode"

  xhtml "golang.org/x/net/html"
  "golang.org/x/net/html/atom"
)

// Removal describes something the sanitizer dropped, so the importer can report it.
type Removal struct {
  Element   string `json:"element"`
  Attribute string `json:"attribute,omitempty"`
  Reason    string `json:"reason"`
}

// IframeHosts are the only hosts an <iframe src> may point at.
var IframeHosts = map[string]bool{
  "www.youtube-nocookie.com": true,
  "www.youtube.com":          true,
  "player.vimeo.com":         true,
  "open.spotify.com":         true,
  "w.soundcloud.com":         true,
  "www.google.com":           true,
  "www.instagram.com":        true,
  "platform.twitter.com":     true,
}

const IframeSandbox = "allow-scripts allow-same-origin allow-popups allow-presentation"

var dropWithContent = map[atom.Atom]bool{
  atom.Script: true, atom.Style: true, atom.Template: true, atom.Noscript: true,
  atom.Object: true, atom.Embed: true, atom.Applet: true, atom.Base: true,
  atom.Form: true, atom.Input: true, atom.Button: true, atom.Select: true, atom.Textarea: true,
  atom.Link: true, atom.Meta: true, atom.Title: true, atom.Head: true,
  atom.Frame: true, atom.Frameset: true, atom.Svg: true, atom.Math: true,
}

var globalAttrs = map[string]bool{"class": true, "id": true, "title": true, "lang": true, "dir": true, "style": true}

var elementAttrs = map[atom.Atom]map[string]bool{
  atom.P: nil, atom.Br: nil, atom.Hr: nil, atom.Span: nil, atom.Div: nil,
  atom.H1: nil, atom.H2: nil, atom.H3: nil, atom.H4: nil, atom.H5: nil, atom.H6: nil,
  atom.Strong: nil, atom.B: nil, atom.Em: nil, atom.I: nil, atom.U: nil, atom.S: nil,
  atom.Strike: nil, atom.Del: nil, atom.Ins: nil, atom.Sub: nil, atom.Sup: nil,
  atom.Code: nil, atom.Pre: nil, atom.Kbd: nil, atom.Mark: nil, atom.Small: nil,
  atom.Abbr: nil, atom.Cite: nil, atom.Dl: nil, atom.Dt: nil, atom.Dd: nil, atom.Ul: nil,
  atom.Figure: nil, atom.Figcaption: nil, atom.Picture: nil, atom.Caption: nil,
  atom.Table: nil, atom.Thead: nil, atom.Tbody: nil, atom.Tfoot: nil, atom.Tr: nil,
  atom.Section: nil, atom.Article: nil, atom.Aside: nil, atom.Header: nil, atom.Footer: nil,
  atom.Summary: nil,
  atom.Blockquote: {"cite": true},
  atom.Q:          {"cite": true},
  atom.Time:       {"datetime": true},
  atom.Details:    {"open": true},
  atom.Ol:         {"start": true, "reversed": true, "type": true},
  atom.Li:         {"value": true},
  atom.Col:        {"span": true},
  atom.Colgroup:   {"span": true},
  atom.Td:         {"colspan": true, "rowspan": true, "headers": true},
  atom.Th:         {"colspan": true, "rowspan": true, "headers": true, "scope": true},
  atom.A:          {"href": true, "target": true, "rel": true, "name": true},
  atom.Img:        {"src": true, "alt": true, "width": true, "height": true, "srcset": true, "sizes": true, "loading": true, "decoding": true},
  atom.Source:     {"src": true, "srcset": true, "type": true, "media": true, "sizes": true},
  atom.Video:      {"src": true, "poster": true, "controls": true, "autoplay": true, "loop": true, "muted": true, "playsinline": true, "preload": true, "width": true, "height": true},
  atom.Audio:      {"src": true, "controls": true, "autoplay": true, "loop": true, "muted": true, "preload": true},
  atom.Iframe:     {"src": true, "width": true, "height": true, "allow": true, "allowfullscreen": true, "loading": true, "frameborder": true},
}

var urlAttrs = map[string]bool{"href": true, "src": true, "cite": true, "poster": true}

var cssProps = map[string]bool{"text-align": true, "width": true, "height": true, "color": true, "background-color": true}

var cssValueRe = regexp.MustCompile(`^[a-zA-Z0-9#%.,() -]+$`)

// Trusted sanitizes HTML for rawHtml blocks and imported markup.
func Trusted(src string) (string, []Removal) {
  body := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
  nodes, err := xhtml.ParseFragment(strings.NewReader(src), body)
  if err != nil {
    return "", []Removal{{Element: "#document", Reason: "unparseable html"}}
  }
  for _, n := range nodes {
    body.AppendChild(n)
  }

  s := &sanitizer{}
  s.clean(body)

  var b strings.Builder
  for c := body.FirstChild; c != nil; c = c.NextSibling {
    if err := xhtml.Render(&b, c); err != nil {
      return "", append(s.removed, Removal{Element: "#document", Reason: "render failed"})
    }
  }
  return b.String(), s.removed
}

// Text returns the visible text of an HTML fragment, for search and word counts.
func Text(src string) string {
  z := xhtml.NewTokenizer(strings.NewReader(src))
  var parts []string
  for {
    switch z.Next() {
    case xhtml.ErrorToken:
      return strings.Join(parts, " ")
    case xhtml.TextToken:
      if t := strings.TrimSpace(string(z.Text())); t != "" {
        parts = append(parts, t)
      }
    }
  }
}

type sanitizer struct {
  removed []Removal
}

func (s *sanitizer) note(el, attr, reason string) {
  s.removed = append(s.removed, Removal{Element: el, Attribute: attr, Reason: reason})
}

func (s *sanitizer) clean(parent *xhtml.Node) {
  for c := parent.FirstChild; c != nil; {
    next := c.NextSibling
    switch c.Type {
    case xhtml.TextNode:
    case xhtml.ElementNode:
      next = s.cleanElement(parent, c, next)
    case xhtml.CommentNode:
      parent.RemoveChild(c)
    default:
      s.note("#node", "", "not allowed")
      parent.RemoveChild(c)
    }
    c = next
  }
}

// cleanElement returns the node to continue with; when c is unwrapped that is its first former child.
func (s *sanitizer) cleanElement(parent, c, next *xhtml.Node) *xhtml.Node {
  name := strings.ToLower(c.Data)
  if c.Namespace != "" || dropWithContent[c.DataAtom] {
    s.note(name, "", "element not allowed")
    parent.RemoveChild(c)
    return next
  }
  allowed, ok := elementAttrs[c.DataAtom]
  if !ok || c.DataAtom == 0 {
    s.note(name, "", "element unwrapped")
    first := c.FirstChild
    for ch := c.FirstChild; ch != nil; {
      n := ch.NextSibling
      c.RemoveChild(ch)
      parent.InsertBefore(ch, c)
      ch = n
    }
    parent.RemoveChild(c)
    if first != nil {
      return first
    }
    return next
  }

  if c.DataAtom == atom.Iframe && !iframeAllowed(c) {
    s.note(name, "src", "iframe host not allowed")
    parent.RemoveChild(c)
    return next
  }

  attrs := make([]xhtml.Attribute, 0, len(c.Attr))
  blank := false
  for _, a := range c.Attr {
    key := strings.ToLower(a.Key)
    if a.Namespace != "" || !(globalAttrs[key] || allowed[key]) {
      s.note(name, key, "attribute not allowed")
      continue
    }
    switch {
    case key == "style":
      css, dropped := cleanStyle(a.Val)
      if dropped {
        s.note(name, key, "css property or value not allowed")
      }
      if css == "" {
        continue
      }
      a.Val = css
    case urlAttrs[key]:
      if !safeURL(a.Val, c.DataAtom == atom.Img && key == "src") {
        s.note(name, key, "url scheme not allowed")
        continue
      }
    case key == "srcset":
      if !safeSrcset(a.Val) {
        s.note(name, key, "url scheme not allowed")
        continue
      }
    case key == "target":
      blank = a.Val == "_blank"
    }
    a.Key = key
    attrs = append(attrs, a)
  }
  c.Attr = attrs

  if blank {
    ensureRel(c, "noopener")
  }
  if c.DataAtom == atom.Iframe {
    setAttr(c, "sandbox", IframeSandbox)
  }
  s.clean(c)
  return next
}

func iframeAllowed(n *xhtml.Node) bool {
  for _, a := range n.Attr {
    if strings.ToLower(a.Key) == "src" {
      return IframeSrcAllowed(a.Val)
    }
  }
  return false
}

// IframeSrcAllowed reports whether src is an https URL on one of IframeHosts.
func IframeSrcAllowed(src string) bool {
  rest, ok := strings.CutPrefix(strings.ToLower(stripControl(src)), "https://")
  if !ok {
    return false
  }
  host, _, _ := strings.Cut(rest, "/")
  host, _, _ = strings.Cut(host, "?")
  return IframeHosts[host]
}

func setAttr(n *xhtml.Node, key, val string) {
  for i, a := range n.Attr {
    if a.Key == key {
      n.Attr[i].Val = val
      return
    }
  }
  n.Attr = append(n.Attr, xhtml.Attribute{Key: key, Val: val})
}

func ensureRel(n *xhtml.Node, token string) {
  for i, a := range n.Attr {
    if a.Key != "rel" {
      continue
    }
    for _, f := range strings.Fields(a.Val) {
      if f == token {
        return
      }
    }
    n.Attr[i].Val = strings.TrimSpace(a.Val + " " + token)
    return
  }
  n.Attr = append(n.Attr, xhtml.Attribute{Key: "rel", Val: token})
}

func stripControl(v string) string {
  return strings.Map(func(r rune) rune {
    if unicode.IsSpace(r) || unicode.IsControl(r) {
      return -1
    }
    return r
  }, v)
}

// SafeURL reports whether v is relative, an #anchor, or uses http, https, mailto or tel.
func SafeURL(v string) bool {
  return safeURL(v, false)
}

func safeURL(v string, allowDataImage bool) bool {
  v = stripControl(v)
  i := strings.IndexAny(v, ":/?#")
  if i < 0 || v[i] != ':' {
    return true
  }
  switch strings.ToLower(v[:i]) {
  case "http", "https", "mailto", "tel":
    return true
  case "data":
    rest := strings.ToLower(v[i+1:])
    return allowDataImage && strings.HasPrefix(rest, "image/") && !strings.HasPrefix(rest, "image/svg")
  }
  return false
}

func safeSrcset(v string) bool {
  for _, cand := range strings.Split(v, ",") {
    if f := strings.Fields(cand); len(f) > 0 && !safeURL(f[0], false) {
      return false
    }
  }
  return true
}

func cleanStyle(v string) (string, bool) {
  var out []string
  dropped := false
  for _, decl := range strings.Split(v, ";") {
    if strings.TrimSpace(decl) == "" {
      continue
    }
    prop, val, ok := strings.Cut(decl, ":")
    prop = strings.ToLower(strings.TrimSpace(prop))
    val = strings.TrimSpace(val)
    lv := strings.ToLower(val)
    if !ok || !cssProps[prop] || !cssValueRe.MatchString(val) ||
      strings.Contains(lv, "url(") || strings.Contains(lv, "expression") {
      dropped = true
      continue
    }
    out = append(out, prop+": "+val)
  }
  return strings.Join(out, "; "), dropped
}

var autolinkRe = regexp.MustCompile(`https?://[^\s<>"']+`)

// Comment renders plain comment text as escaped HTML with autolinked http(s) URLs.
func Comment(text string) string {
  var b strings.Builder
  last := 0
  for _, m := range autolinkRe.FindAllStringIndex(text, -1) {
    u := strings.TrimRight(text[m[0]:m[1]], ".,;:!?)")
    b.WriteString(html.EscapeString(text[last:m[0]]))
    b.WriteString(`<a href="` + html.EscapeString(u) + `" rel="nofollow ugc noopener">` + html.EscapeString(u) + `</a>`)
    last = m[0] + len(u)
  }
  b.WriteString(html.EscapeString(text[last:]))
  return strings.ReplaceAll(b.String(), "\n", "<br>\n")
}
