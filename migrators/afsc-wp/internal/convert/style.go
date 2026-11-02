package convert

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	headingIDRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_:.-]{0,100}$`)
	relRe       = regexp.MustCompile(`^[a-z ]{1,64}$`)
	codeLangRe  = regexp.MustCompile(`^[A-Za-z0-9_+#.-]{1,32}$`)
	classRe     = regexp.MustCompile(`^[A-Za-z_-][A-Za-z0-9_-]{0,63}$`)
	sizeNameRe  = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	hexRe       = regexp.MustCompile(`^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	rgbRe       = regexp.MustCompile(`^rgb\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*\)$`)
)

// hardMechanic classes become attributes (align, size, media id) and are never kept as classes.
var hardMechanic = regexp.MustCompile(`^(align(left|right|center|none)|wp-image-\d+|size-[a-z0-9_-]+|has-text-align-(left|center|right|justify))$`)

// mechanicClass are WordPress-generated classes; kept only when the theme allows them, never flagged.
var mechanicClass = regexp.MustCompile(`^(align(left|right|center|none|wide|full)|wp-image-\d+|size-[a-z0-9_-]+|attachment-[a-z0-9_-]+|wp-caption(-text)?|gallery(-item|-icon|-caption|-columns-\d+|-size-[a-z0-9_-]+)?|galleryid-\d+|wp-block-[a-z0-9_-]+|has-text-align-(left|center|right|justify)|is-style-default|is-layout-[a-z]+|wp-embed-aspect-[0-9-]+|wp-has-aspect-ratio|is-type-[a-z]+|is-provider-[a-z0-9-]+|blocks-gallery-[a-z-]+|has-nested-images|columns-\d+|is-cropped)$`)

// keepClasses returns the allowed classes of n, flagging the rest. skip filters classes handled elsewhere.
func (w *walker) keepClasses(n *html.Node, skip func(string) bool) string {
	var kept []string
	for _, c := range classes(n) {
		switch {
		case hardMechanic.MatchString(c), skip != nil && skip(c):
		case classRe.MatchString(c) && w.ctx.AllowedClasses[c]:
			kept = append(kept, c)
		case mechanicClass.MatchString(c):
		default:
			w.flag("class", "%s", c)
		}
	}
	return strings.Join(kept, " ")
}

// textAlign reads text-align from a style attribute or a block-editor class, flagging other properties.
func (w *walker) textAlign(n *html.Node) string {
	align := ""
	for _, c := range classes(n) {
		if a, ok := strings.CutPrefix(c, "has-text-align-"); ok {
			align = a
		}
	}
	for _, d := range parseStyle(attr(n, "style")) {
		if d.prop == "text-align" {
			switch d.val {
			case "left", "center", "right", "justify":
				align = d.val
			default:
				w.flag("style", "text-align: %s", d.val)
			}
			continue
		}
		w.flag("style", "%s", d.prop)
	}
	return align
}

type decl struct{ prop, val string }

func parseStyle(s string) []decl {
	var out []decl
	for _, part := range strings.Split(s, ";") {
		p, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		p, v = strings.ToLower(strings.TrimSpace(p)), strings.TrimSpace(v)
		v = strings.TrimSpace(strings.TrimSuffix(v, "!important"))
		if p != "" {
			out = append(out, decl{p, strings.ToLower(v)})
		}
	}
	return out
}

var namedColors = map[string]string{
	"black": "#000000", "white": "#ffffff", "red": "#ff0000", "green": "#008000", "blue": "#0000ff",
	"yellow": "#ffff00", "orange": "#ffa500", "purple": "#800080", "gray": "#808080", "grey": "#808080",
	"silver": "#c0c0c0", "maroon": "#800000", "olive": "#808000", "lime": "#00ff00", "aqua": "#00ffff",
	"teal": "#008080", "navy": "#000080", "fuchsia": "#ff00ff",
}

// cssColor returns a color AFSC accepts (hex or rgb()), converting basic named colors.
func cssColor(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case hexRe.MatchString(v), rgbRe.MatchString(v):
		return v, true
	case namedColors[v] != "":
		return namedColors[v], true
	}
	return "", false
}

func alignOf(n *html.Node) string {
	for _, c := range classes(n) {
		switch c {
		case "alignleft":
			return "left"
		case "alignright":
			return "right"
		case "aligncenter":
			return "center"
		case "alignnone":
			return "none"
		}
	}
	return ""
}

func alignWord(s string) string {
	switch strings.TrimPrefix(strings.ToLower(s), "align") {
	case "left":
		return "left"
	case "right":
		return "right"
	case "center":
		return "center"
	case "none":
		return "none"
	}
	return ""
}
