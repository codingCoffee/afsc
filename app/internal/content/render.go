package content

import (
  "fmt"
  "html"
  "net/url"
  "reflect"
  "slices"
  "strconv"
  "strings"

  "afsc/internal/sanitize"
)

type MediaInfo struct {
  ID       int64
  LegacyID int64 // numeric WordPress id from a wp:post:N legacy key, 0 when none
  Filename string
  Version  string // first 8 hex chars of the sha256
  Mime     string
  Width    int
  Height   int
}

type RenderContext struct {
  PostID int64
  Media  func(id int64) (MediaInfo, bool)
}

// MediaURL is the public URL of a media variant (§9).
func MediaURL(m MediaInfo, variant string) string {
  return fmt.Sprintf("/m/%d/%s/%s/%s", m.ID, m.Version, variant, url.PathEscape(m.Filename))
}

// WPID is the id used in WordPress-compatible classes such as wp-image-{id}.
func (m MediaInfo) WPID() int64 {
  if m.LegacyID > 0 {
    return m.LegacyID
  }
  return m.ID
}

// Render is the only code that turns content into HTML. It returns the HTML and the plain text.
func Render(doc *Node, ctx RenderContext) (string, string, error) {
  r := &renderer{ctx: ctx}
  if err := r.block(doc); err != nil {
    return "", "", err
  }
  return r.b.String(), strings.TrimSpace(r.t.String()), nil
}

type renderer struct {
  ctx    RenderContext
  b      strings.Builder
  t      strings.Builder
  images int
}

func (r *renderer) w(s string) { r.b.WriteString(s) }

func (r *renderer) text(s string) {
  r.b.WriteString(html.EscapeString(s))
  r.t.WriteString(s)
}

func (r *renderer) textBreak() {
  if s := r.t.String(); s != "" && !strings.HasSuffix(s, "\n") {
    r.t.WriteString("\n")
  }
}

// open writes a start tag. kv are name/value pairs; empty values are skipped except alt.
func (r *renderer) open(tag string, kv ...string) {
  r.w("<" + tag)
  for i := 0; i+1 < len(kv); i += 2 {
    if kv[i+1] == "" && kv[i] != "alt" {
      continue
    }
    r.w(" " + kv[i] + `="` + html.EscapeString(kv[i+1]) + `"`)
  }
  r.w(">")
}

func (r *renderer) close(tag string) { r.w("</" + tag + ">") }

func cls(parts ...string) string {
  return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), " ")
}

func alignStyle(n *Node) string {
  if a := n.str("textAlign"); a != "" {
    return "text-align: " + a + ";"
  }
  return ""
}

func itoa(i int64) string {
  if i == 0 {
    return ""
  }
  return strconv.FormatInt(i, 10)
}

func (r *renderer) children(n *Node) error {
  for _, c := range n.Content {
    if err := r.block(c); err != nil {
      return err
    }
  }
  return nil
}

func (r *renderer) wrap(n *Node, tag string, kv ...string) error {
  r.open(tag, kv...)
  if err := r.children(n); err != nil {
    return err
  }
  r.close(tag)
  return nil
}

func (r *renderer) textblock(n *Node, tag string, kv ...string) error {
  r.open(tag, kv...)
  if err := r.inline(n.Content); err != nil {
    return err
  }
  r.close(tag)
  r.textBreak()
  return nil
}

func (r *renderer) block(n *Node) error {
  switch n.Type {
  case "doc":
    return r.children(n)
  case "paragraph":
    return r.textblock(n, "p", "style", alignStyle(n), "class", n.str("classes"))
  case "heading":
    level, _ := n.int("level")
    tag := "h" + strconv.FormatInt(level, 10)
    return r.textblock(n, tag, "id", n.str("id"), "style", alignStyle(n), "class", n.str("classes"))
  case "blockquote":
    return r.wrap(n, "blockquote", "class", n.str("classes"))
  case "bulletList":
    return r.wrap(n, "ul")
  case "orderedList":
    start := ""
    if s, ok := n.int("start"); ok && s != 1 {
      start = strconv.FormatInt(s, 10)
    }
    return r.wrap(n, "ol", "start", start)
  case "listItem":
    return r.wrap(n, "li")
  case "codeBlock":
    r.w("<pre>")
    lang := ""
    if l := n.str("language"); l != "" {
      lang = "language-" + l
    }
    r.open("code", "class", lang)
    for _, c := range n.Content {
      r.text(c.Text)
    }
    r.w("</code></pre>")
    r.textBreak()
  case "horizontalRule":
    r.w("<hr>")
  case "image":
    _, err := r.image(n, imageOpts{})
    r.textBreak()
    return err
  case "figure":
    return r.figure(n)
  case "gallery":
    return r.gallery(n)
  case "embed":
    r.embed(n)
  case "video":
    return r.video(n)
  case "table":
    r.w("<table><tbody>")
    if err := r.children(n); err != nil {
      return err
    }
    r.w("</tbody></table>")
  case "tableRow":
    return r.wrap(n, "tr")
  case "tableCell", "tableHeader":
    tag := "td"
    if n.Type == "tableHeader" {
      tag = "th"
    }
    span := func(k string) string {
      if v, ok := n.int(k); ok && v > 1 {
        return strconv.FormatInt(v, 10)
      }
      return ""
    }
    return r.wrap(n, tag, "colspan", span("colspan"), "rowspan", span("rowspan"))
  case "details":
    return r.wrap(n, "details")
  case "detailsSummary":
    return r.textblock(n, "summary")
  case "detailsContent":
    return r.wrap(n, "div", "class", "details-content")
  case "container":
    return r.wrap(n, "div", "class", n.str("classes"))
  case "readMore":
    id := "more"
    if r.ctx.PostID > 0 {
      id = "more-" + strconv.FormatInt(r.ctx.PostID, 10)
    }
    r.open("span", "id", id)
    r.close("span")
  case "rawHtml":
    clean, _ := sanitize.Trusted(n.str("html"))
    r.w(clean)
    r.t.WriteString(sanitize.Text(clean))
    r.textBreak()
  default:
    return fmt.Errorf("cannot render node type %q", n.Type)
  }
  return nil
}

func (r *renderer) media(n *Node, key string) (MediaInfo, bool, error) {
  id, ok := n.int(key)
  if !ok {
    return MediaInfo{}, false, nil
  }
  if r.ctx.Media == nil {
    return MediaInfo{}, false, fmt.Errorf("no media lookup for media %d", id)
  }
  m, found := r.ctx.Media(id)
  if !found {
    return MediaInfo{}, false, fmt.Errorf("unknown media %d", id)
  }
  return m, true, nil
}

type imageOpts struct {
  inFigure    bool
  galleryLink string
  gallerySize string
}

type imageResult struct {
  media    MediaInfo
  hasMedia bool
  width    int64
}

func (r *renderer) image(n *Node, o imageOpts) (imageResult, error) {
  m, hasMedia, err := r.media(n, "mediaId")
  if err != nil {
    return imageResult{}, err
  }
  src := n.str("src")
  width, _ := n.int("width")
  height, _ := n.int("height")
  if hasMedia {
    src = MediaURL(m, "orig")
    if width == 0 {
      width = int64(m.Width)
    }
    if height == 0 {
      height = int64(m.Height)
    }
  }

  size := n.str("size")
  if o.gallerySize != "" {
    size = o.gallerySize
  }
  var classes []string
  if hasMedia {
    if size == "" {
      size = "full"
    }
    classes = append(classes, "wp-image-"+strconv.FormatInt(m.WPID(), 10))
  }
  if size != "" {
    classes = append(classes, "size-"+size)
  }
  if a := n.str("align"); a != "" && !o.inFigure && o.galleryLink == "" {
    classes = append(classes, "align"+a)
  }
  classes = append(classes, n.classes()...)

  href := n.str("href")
  target := n.str("linkTarget")
  if o.galleryLink != "" {
    target, href = o.galleryLink, ""
  }
  switch {
  case target == "none":
    href = ""
  case target == "media" && hasMedia:
    href = MediaURL(m, "orig")
  case target == "media":
    href = src
  }

  r.images++
  loading, decoding := "", ""
  if r.images > 1 {
    loading, decoding = "lazy", "async"
  }
  if href != "" {
    r.open("a", "href", href)
  }
  r.open("img", "src", src, "alt", n.str("alt"), "title", n.str("title"), "width", itoa(width), "height", itoa(height),
    "class", cls(classes...), "loading", loading, "decoding", decoding)
  if href != "" {
    r.close("a")
  }
  return imageResult{media: m, hasMedia: hasMedia, width: width}, nil
}

func (r *renderer) figure(n *Node) error {
  img, caption := n.Content[0], n.Content[1]
  align := n.str("align")
  if align == "" {
    align = img.str("align")
  }
  if align == "" {
    align = "none"
  }

  // the figure's attributes depend on the image, so render the image with a scratch renderer first
  sub := &renderer{ctx: r.ctx, images: r.images}
  res, err := sub.image(img, imageOpts{inFigure: true})
  if err != nil {
    return err
  }
  r.images = sub.images
  imgHTML := sub.b.String()

  id, captionID, style := "", "", ""
  if res.hasMedia {
    wpid := strconv.FormatInt(res.media.WPID(), 10)
    id, captionID = "attachment_"+wpid, "caption-attachment-"+wpid
  }
  if res.width > 0 {
    style = "width: " + strconv.FormatInt(res.width, 10) + "px"
  }
  r.open("figure", "id", id, "aria-describedby", captionID, "style", style,
    "class", cls("wp-caption", "align"+align, n.str("classes")))
  r.w(imgHTML)
  r.textBreak()
  if err := r.textblock(caption, "figcaption", "id", captionID, "class", "wp-caption-text"); err != nil {
    return err
  }
  r.close("figure")
  return nil
}

func (r *renderer) gallery(n *Node) error {
  cols, _ := n.int("columns")
  size := n.str("size")
  if size == "" {
    size = "thumbnail"
  }
  link := n.str("link")
  if link == "" {
    link = "none"
  }
  galleryID := ""
  if r.ctx.PostID > 0 {
    galleryID = "galleryid-" + strconv.FormatInt(r.ctx.PostID, 10)
  }
  r.open("div", "class", cls("gallery", galleryID, "gallery-columns-"+strconv.FormatInt(cols, 10), "gallery-size-"+size, n.str("classes")))
  for _, img := range n.Content {
    orientation := "landscape"
    w, _ := img.int("width")
    h, _ := img.int("height")
    if m, ok, _ := r.media(img, "mediaId"); ok && w == 0 {
      w, h = int64(m.Width), int64(m.Height)
    }
    if h > w {
      orientation = "portrait"
    }
    r.w(`<figure class="gallery-item">`)
    r.open("div", "class", "gallery-icon "+orientation)
    if _, err := r.image(img, imageOpts{galleryLink: link, gallerySize: size}); err != nil {
      return err
    }
    r.w("</div></figure>")
  }
  r.close("div")
  r.textBreak()
  return nil
}

var embedTitles = map[string]string{
  "youtube": "YouTube", "vimeo": "Vimeo", "x": "X", "instagram": "Instagram",
  "spotify": "Spotify", "soundcloud": "SoundCloud", "googlemaps": "Google Maps",
}

func (r *renderer) embed(n *Node) {
  provider, u, caption := n.str("provider"), n.str("url"), n.str("caption")
  title := caption
  if title == "" {
    title = embedTitles[provider] + " embed"
  }
  width, _ := n.int("width")
  height, _ := n.int("height")

  r.open("figure", "class", "wp-block-embed is-provider-"+provider+" wp-block-embed-"+provider)
  r.w(`<div class="wp-block-embed__wrapper">`)
  iframe := func(src, allow string) {
    r.open("iframe", "src", src, "title", title, "width", itoa(width), "height", itoa(height),
      "loading", "lazy", "allow", allow, "allowfullscreen", "true", "sandbox", sanitize.IframeSandbox)
    r.close("iframe")
  }
  id := n.str("externalId")
  switch provider {
  case "youtube":
    iframe("https://www.youtube-nocookie.com/embed/"+url.PathEscape(id), "encrypted-media; picture-in-picture")
  case "vimeo":
    iframe("https://player.vimeo.com/video/"+url.PathEscape(id), "fullscreen; picture-in-picture")
  case "spotify":
    iframe(strings.Replace(spotifyRe.FindString(u), "open.spotify.com/", "open.spotify.com/embed/", 1), "encrypted-media")
  case "soundcloud":
    iframe("https://w.soundcloud.com/player/?url="+url.QueryEscape(u), "")
  case "googlemaps":
    iframe(u, "")
  case "x":
    r.w(`<blockquote class="twitter-tweet">`)
    r.open("a", "href", u)
    r.text(u)
    r.w("</a></blockquote>")
  case "instagram":
    r.open("blockquote", "class", "instagram-media", "data-instgrm-permalink", u)
    r.open("a", "href", u)
    r.text(u)
    r.w("</a></blockquote>")
  }
  r.w("</div>")
  r.textBreak()
  if caption != "" {
    r.w("<figcaption>")
    r.text(caption)
    r.w("</figcaption>")
    r.textBreak()
  }
  r.close("figure")
}

func (r *renderer) video(n *Node) error {
  m, hasMedia, err := r.media(n, "mediaId")
  if err != nil {
    return err
  }
  src := n.str("src")
  if hasMedia {
    src = MediaURL(m, "orig")
  }
  poster := ""
  if p, ok, err := r.media(n, "posterMediaId"); err != nil {
    return err
  } else if ok {
    poster = MediaURL(p, "orig")
  }
  flag := func(k string) string {
    if n.bool(k) {
      return k
    }
    return ""
  }
  r.w(`<figure class="wp-block-video">`)
  r.open("video", "src", src, "poster", poster, "controls", flag("controls"), "autoplay", flag("autoplay"),
    "loop", flag("loop"), "muted", flag("muted"), "preload", "metadata")
  r.w("</video></figure>")
  return nil
}

func markRank(t string) int { return slices.Index(MarkOrder, t) }

func sameMark(a, b *Mark) bool {
  return a.Type == b.Type && reflect.DeepEqual(a.Attrs, b.Attrs)
}

// inline renders inline children, keeping marks shared by adjacent nodes open, like ProseMirror's serializer.
func (r *renderer) inline(children []*Node) error {
  var open []*Mark
  for _, c := range children {
    marks := slices.Clone(c.Marks)
    slices.SortStableFunc(marks, func(a, b *Mark) int { return markRank(a.Type) - markRank(b.Type) })

    keep := 0
    for keep < len(open) && keep < len(marks) && sameMark(open[keep], marks[keep]) {
      keep++
    }
    for i := len(open) - 1; i >= keep; i-- {
      r.closeMark(open[i])
    }
    open = open[:keep]
    for _, m := range marks[keep:] {
      r.openMark(m)
      open = append(open, m)
    }

    switch c.Type {
    case "text":
      r.text(c.Text)
    case "hardBreak":
      r.w("<br>")
      r.t.WriteString("\n")
    default:
      return fmt.Errorf("cannot render inline node type %q", c.Type)
    }
  }
  for i := len(open) - 1; i >= 0; i-- {
    r.closeMark(open[i])
  }
  return nil
}

var simpleMarkTags = map[string]string{
  "bold": "strong", "italic": "em", "underline": "u", "strike": "s", "code": "code", "subscript": "sub", "superscript": "sup",
}

func markStr(m *Mark, k string) string {
  s, _ := m.Attrs[k].(string)
  return s
}

func (r *renderer) openMark(m *Mark) {
  switch m.Type {
  case "link":
    rel, target := markStr(m, "rel"), markStr(m, "target")
    if target == "_blank" && !slices.Contains(strings.Fields(rel), "noopener") {
      rel = cls(rel, "noopener")
    }
    r.open("a", "href", markStr(m, "href"), "target", target, "rel", rel, "title", markStr(m, "title"))
  case "textStyle":
    var style []string
    if c := markStr(m, "color"); c != "" {
      style = append(style, "color: "+c+";")
    }
    if c := markStr(m, "backgroundColor"); c != "" {
      style = append(style, "background-color: "+c+";")
    }
    r.open("span", "style", strings.Join(style, " "))
  default:
    r.w("<" + simpleMarkTags[m.Type] + ">")
  }
}

func (r *renderer) closeMark(m *Mark) {
  switch m.Type {
  case "link":
    r.close("a")
  case "textStyle":
    r.close("span")
  default:
    r.close(simpleMarkTags[m.Type])
  }
}
