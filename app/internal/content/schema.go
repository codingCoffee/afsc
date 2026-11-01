package content

import (
	"encoding/json"
	"regexp"
)

type AttrType string

const (
	TString  AttrType = "string"
	TInt     AttrType = "integer"
	TBool    AttrType = "boolean"
	TEnum    AttrType = "enum"
	TURL     AttrType = "url"
	TClasses AttrType = "classes"
	TColor   AttrType = "color"
	TIntList AttrType = "integer[]"
	TMediaID AttrType = "mediaId"
)

type AttrSpec struct {
	Type     AttrType `json:"type"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Min      *int64   `json:"min,omitempty"`
	Max      *int64   `json:"max,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
}

// ChildRule matches between Min and Max (-1 = unbounded) consecutive children whose type or group is in Allow.
type ChildRule struct {
	Allow []string `json:"allow"`
	Min   int      `json:"min"`
	Max   int      `json:"max"`
}

type NodeSpec struct {
	Group   string              `json:"group,omitempty"`
	Content []ChildRule         `json:"content,omitempty"`
	Attrs   map[string]AttrSpec `json:"attrs,omitempty"`
	Marks   bool                `json:"marks,omitempty"`
}

type MarkSpec struct {
	Attrs map[string]AttrSpec `json:"attrs,omitempty"`
}

var EmbedProviders = []string{"youtube", "vimeo", "x", "instagram", "spotify", "soundcloud", "googlemaps"}

func rng(min, max int64) AttrSpec { return AttrSpec{Type: TInt, Min: &min, Max: &max} }
func enum(v ...string) AttrSpec   { return AttrSpec{Type: TEnum, Enum: v} }
func pat(p string) AttrSpec       { return AttrSpec{Type: TString, Pattern: p} }
func req(a AttrSpec) AttrSpec     { a.Required = true; return a }

var (
	str       = AttrSpec{Type: TString}
	boolean   = AttrSpec{Type: TBool}
	link      = AttrSpec{Type: TURL}
	classes   = AttrSpec{Type: TClasses}
	mediaID   = AttrSpec{Type: TMediaID}
	dimension = rng(1, 100000)
	textAlign = enum("left", "center", "right", "justify")
	align     = enum("none", "left", "center", "right")
	sizeName  = pat(`^[a-z0-9_-]{1,40}$`)
)

func many(allow ...string) []ChildRule { return []ChildRule{{Allow: allow, Min: 0, Max: -1}} }
func some(allow ...string) []ChildRule { return []ChildRule{{Allow: allow, Min: 1, Max: -1}} }
func one(t string) ChildRule           { return ChildRule{Allow: []string{t}, Min: 1, Max: 1} }

var cellAttrs = map[string]AttrSpec{"colspan": rng(1, 1000), "rowspan": rng(1, 1000), "colwidth": {Type: TIntList}}

// Nodes is the authoritative content schema (§5.1). The Tiptap side asserts against SchemaJSON.
var Nodes = map[string]NodeSpec{
	"doc":            {Content: some("block")},
	"paragraph":      {Group: "block", Content: many("inline"), Marks: true, Attrs: map[string]AttrSpec{"textAlign": textAlign, "classes": classes}},
	"heading":        {Group: "block", Content: many("inline"), Marks: true, Attrs: map[string]AttrSpec{"level": req(rng(1, 6)), "textAlign": textAlign, "id": pat(`^[A-Za-z][A-Za-z0-9_:.-]{0,100}$`), "classes": classes}},
	"blockquote":     {Group: "block", Content: some("block"), Attrs: map[string]AttrSpec{"classes": classes}},
	"bulletList":     {Group: "block", Content: some("listItem")},
	"orderedList":    {Group: "block", Content: some("listItem"), Attrs: map[string]AttrSpec{"start": rng(0, 1000000)}},
	"listItem":       {Content: some("block")},
	"codeBlock":      {Group: "block", Content: many("text"), Attrs: map[string]AttrSpec{"language": pat(`^[A-Za-z0-9_+#.-]{1,32}$`)}},
	"horizontalRule": {Group: "block"},
	"image": {Group: "block", Attrs: map[string]AttrSpec{
		"mediaId": mediaID, "src": link, "alt": req(str), "title": str, "width": dimension, "height": dimension,
		"align": align, "size": sizeName, "href": link, "linkTarget": enum("media", "custom", "none"), "classes": classes,
	}},
	"figure":     {Group: "block", Content: []ChildRule{one("image"), one("figcaption")}, Attrs: map[string]AttrSpec{"align": align, "classes": classes}},
	"figcaption": {Content: many("inline"), Marks: true},
	"gallery": {Group: "block", Content: some("image"), Attrs: map[string]AttrSpec{
		"columns": req(rng(1, 9)), "size": sizeName, "link": enum("media", "none"), "classes": classes,
	}},
	"embed": {Group: "block", Attrs: map[string]AttrSpec{
		"provider": req(enum(EmbedProviders...)), "url": req(link), "externalId": pat(`^[A-Za-z0-9_-]{1,64}$`),
		"width": dimension, "height": dimension, "caption": str,
	}},
	"video": {Group: "block", Attrs: map[string]AttrSpec{
		"mediaId": mediaID, "src": link, "posterMediaId": mediaID,
		"controls": boolean, "autoplay": boolean, "loop": boolean, "muted": boolean,
	}},
	"table":          {Group: "block", Content: some("tableRow")},
	"tableRow":       {Content: some("tableCell", "tableHeader")},
	"tableCell":      {Content: some("block"), Attrs: cellAttrs},
	"tableHeader":    {Content: some("block"), Attrs: cellAttrs},
	"details":        {Group: "block", Content: []ChildRule{one("detailsSummary"), one("detailsContent")}},
	"detailsSummary": {Content: many("inline"), Marks: true},
	"detailsContent": {Content: some("block")},
	"container":      {Group: "block", Content: some("block"), Attrs: map[string]AttrSpec{"classes": req(classes)}},
	"readMore":       {Group: "block"},
	"rawHtml":        {Group: "block", Attrs: map[string]AttrSpec{"html": req(str)}},
	"text":           {Group: "inline"},
	"hardBreak":      {Group: "inline"},
}

// MarkOrder is the canonical nesting order of marks, outermost first.
var MarkOrder = []string{"link", "bold", "italic", "underline", "strike", "code", "subscript", "superscript", "textStyle"}

var Marks = map[string]MarkSpec{
	"bold": {}, "italic": {}, "underline": {}, "strike": {}, "code": {}, "subscript": {}, "superscript": {},
	"link": {Attrs: map[string]AttrSpec{
		"href": req(link), "target": enum("_blank", "_self"), "rel": pat(`^[a-z ]{1,64}$`), "title": str,
	}},
	"textStyle": {Attrs: map[string]AttrSpec{"color": {Type: TColor}, "backgroundColor": {Type: TColor}}},
}

var patterns = map[string]*regexp.Regexp{}

func init() {
	add := func(attrs map[string]AttrSpec) {
		for _, a := range attrs {
			if a.Pattern != "" {
				patterns[a.Pattern] = regexp.MustCompile(a.Pattern)
			}
		}
	}
	for _, n := range Nodes {
		add(n.Attrs)
	}
	for _, m := range Marks {
		add(m.Attrs)
	}
}

// SchemaJSON exports the schema so the admin's Tiptap extensions can be checked against it.
func SchemaJSON() ([]byte, error) {
	return json.MarshalIndent(struct {
		Nodes          map[string]NodeSpec `json:"nodes"`
		Marks          map[string]MarkSpec `json:"marks"`
		MarkOrder      []string            `json:"mark_order"`
		EmbedProviders []string            `json:"embed_providers"`
	}{Nodes, Marks, MarkOrder, EmbedProviders}, "", "  ")
}
