package content

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"afsc/internal/sanitize"
)

type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type Removal struct {
	Path string `json:"path"`
	sanitize.Removal
}

type Options struct {
	AllowedClasses map[string]bool
	// DropUnknownClasses turns unknown classes into warnings (import) instead of errors (save).
	DropUnknownClasses bool
	AllowRawHTML       bool
	// MediaExists, when set, checks that mediaId/posterMediaId references resolve.
	MediaExists func(id int64) bool
}

type Result struct {
	Errors   []Issue
	Warnings []Issue
	Removals []Removal
}

func (r Result) OK() bool { return len(r.Errors) == 0 }

var (
	classRe = regexp.MustCompile(`^[A-Za-z_-][A-Za-z0-9_-]{0,63}$`)
	colorRe = regexp.MustCompile(`^(#[0-9a-fA-F]{3,4}|#[0-9a-fA-F]{6}|#[0-9a-fA-F]{8}|rgb\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*\))$`)
)

var embedURLPrefixes = map[string][]string{
	"youtube":    {"https://www.youtube.com/", "https://youtube.com/", "https://youtu.be/", "https://m.youtube.com/"},
	"vimeo":      {"https://vimeo.com/", "https://player.vimeo.com/"},
	"x":          {"https://x.com/", "https://twitter.com/"},
	"instagram":  {"https://www.instagram.com/"},
	"spotify":    {"https://open.spotify.com/"},
	"soundcloud": {"https://soundcloud.com/"},
	"googlemaps": {"https://www.google.com/maps/embed?"},
}

var spotifyRe = regexp.MustCompile(`^https://open\.spotify\.com/(track|album|playlist|episode|show|artist)/[A-Za-z0-9]{1,64}`)

// Validate checks doc against the schema. It normalizes doc in place: unknown classes are dropped when
// DropUnknownClasses is set, and rawHtml is replaced by its sanitized form.
func Validate(doc *Node, opts Options) Result {
	v := &validator{opts: opts}
	if doc == nil || doc.Type != "doc" {
		v.err("", "root node must be of type doc")
		return v.res
	}
	v.node(doc, "", false)
	return v.res
}

type validator struct {
	opts Options
	res  Result
}

func (v *validator) err(path, format string, args ...any) {
	v.res.Errors = append(v.res.Errors, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) warn(path, format string, args ...any) {
	v.res.Warnings = append(v.res.Warnings, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

func join(path, seg string) string {
	if path == "" {
		return seg
	}
	return path + "." + seg
}

func (v *validator) node(n *Node, path string, marksAllowed bool) {
	if n == nil {
		v.err(path, "null node")
		return
	}
	spec, ok := Nodes[n.Type]
	if !ok {
		v.err(path, "unknown node type %q", n.Type)
		return
	}

	if n.Type == "text" {
		if n.Text == "" {
			v.err(path, "text node must not be empty")
		}
		if len(n.Attrs) > 0 || len(n.Content) > 0 {
			v.err(path, "text node must not have attrs or content")
		}
		if len(n.Marks) > 0 && !marksAllowed {
			v.err(join(path, "marks"), "marks are not allowed here")
		}
		v.marks(n, path)
		return
	}
	if n.Text != "" || len(n.Marks) > 0 {
		v.err(path, "only text nodes may have text or marks")
	}

	v.attrs(n.Attrs, spec.Attrs, path)
	v.special(n, path)

	if msg := matchChildren(n.Content, spec.Content); msg != "" {
		v.err(path, "%s: %s", n.Type, msg)
	}
	for i, c := range n.Content {
		v.node(c, join(path, fmt.Sprintf("content[%d]", i)), spec.Marks)
	}
}

func (v *validator) attrs(attrs map[string]any, specs map[string]AttrSpec, path string) {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		p := join(path, "attrs."+k)
		spec, ok := specs[k]
		if !ok {
			v.err(p, "unknown attribute")
			continue
		}
		if attrs[k] == nil {
			continue
		}
		if msg := v.check(attrs, k, spec, p); msg != "" {
			v.err(p, "%s", msg)
		}
	}
	required := make([]string, 0, len(specs))
	for k, spec := range specs {
		if spec.Required && attrs[k] == nil {
			required = append(required, k)
		}
	}
	sort.Strings(required)
	for _, k := range required {
		v.err(join(path, "attrs."+k), "required")
	}
}

func (v *validator) check(attrs map[string]any, k string, spec AttrSpec, path string) string {
	val := attrs[k]
	switch spec.Type {
	case TString:
		s, ok := val.(string)
		if !ok {
			return "must be a string"
		}
		if spec.Pattern != "" && !patterns[spec.Pattern].MatchString(s) {
			return fmt.Sprintf("must match %s", spec.Pattern)
		}
	case TInt:
		i, ok := toInt(val)
		if !ok {
			return "must be an integer"
		}
		if (spec.Min != nil && i < *spec.Min) || (spec.Max != nil && i > *spec.Max) {
			return fmt.Sprintf("must be between %d and %d", *spec.Min, *spec.Max)
		}
	case TBool:
		if _, ok := val.(bool); !ok {
			return "must be a boolean"
		}
	case TEnum:
		s, ok := val.(string)
		if !ok || !slices.Contains(spec.Enum, s) {
			return fmt.Sprintf("must be one of %s", strings.Join(spec.Enum, ", "))
		}
	case TURL:
		s, ok := val.(string)
		if !ok || s == "" || !sanitize.SafeURL(s) {
			return "must be an http(s), mailto, tel, relative or #anchor url"
		}
	case TColor:
		s, ok := val.(string)
		if !ok || !colorRe.MatchString(s) {
			return "must be a hex or rgb() color"
		}
	case TIntList:
		list, ok := val.([]any)
		if !ok {
			return "must be a list of integers"
		}
		for _, x := range list {
			if x == nil {
				continue
			}
			if i, ok := toInt(x); !ok || i < 0 {
				return "must be a list of non-negative integers"
			}
		}
	case TMediaID:
		i, ok := toInt(val)
		if !ok || i < 1 {
			return "must be a positive media id"
		}
		if v.opts.MediaExists != nil && !v.opts.MediaExists(i) {
			return fmt.Sprintf("media %d does not exist", i)
		}
	case TClasses:
		s, ok := val.(string)
		if !ok {
			return "must be a space-separated string"
		}
		var kept []string
		for _, c := range strings.Fields(s) {
			switch {
			case !classRe.MatchString(c):
				v.warnOrErr(path, "invalid class %q", c)
			case !v.opts.AllowedClasses[c]:
				v.warnOrErr(path, "class %q is not in the theme allowlist", c)
			default:
				kept = append(kept, c)
				continue
			}
			if !v.opts.DropUnknownClasses {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			delete(attrs, k)
		} else {
			attrs[k] = strings.Join(kept, " ")
		}
	}
	return ""
}

func (v *validator) warnOrErr(path, format string, args ...any) {
	if v.opts.DropUnknownClasses {
		v.warn(path, format+"; dropped", args...)
	} else {
		v.err(path, format, args...)
	}
}

func (v *validator) special(n *Node, path string) {
	switch n.Type {
	case "image", "video":
		_, hasMedia := n.int("mediaId")
		hasSrc := n.str("src") != ""
		if hasMedia == hasSrc {
			v.err(path, "%s needs exactly one of mediaId or src", n.Type)
		}
		if n.Type == "image" && n.str("linkTarget") == "custom" && n.str("href") == "" {
			v.err(join(path, "attrs.href"), "required when linkTarget is custom")
		}
	case "container":
		if len(n.classes()) == 0 {
			v.err(join(path, "attrs.classes"), "container needs at least one allowed class")
		}
	case "embed":
		provider, url := n.str("provider"), n.str("url")
		if prefixes, ok := embedURLPrefixes[provider]; ok && !hasAnyPrefix(url, prefixes) {
			v.err(join(path, "attrs.url"), "url does not belong to provider %s", provider)
		}
		if (provider == "youtube" || provider == "vimeo") && n.str("externalId") == "" {
			v.err(join(path, "attrs.externalId"), "required for %s", provider)
		}
		if provider == "spotify" && !spotifyRe.MatchString(url) {
			v.err(join(path, "attrs.url"), "unsupported spotify url")
		}
	case "rawHtml":
		if !v.opts.AllowRawHTML {
			v.err(path, "rawHtml blocks are not allowed")
			return
		}
		clean, removed := sanitize.Trusted(n.str("html"))
		n.Attrs["html"] = clean
		for _, r := range removed {
			v.res.Removals = append(v.res.Removals, Removal{Path: path, Removal: r})
		}
	}
}

func (v *validator) marks(n *Node, path string) {
	seen := map[string]bool{}
	for i, m := range n.Marks {
		p := join(path, fmt.Sprintf("marks[%d]", i))
		if m == nil {
			v.err(p, "null mark")
			continue
		}
		spec, ok := Marks[m.Type]
		if !ok {
			v.err(p, "unknown mark type %q", m.Type)
			continue
		}
		if seen[m.Type] {
			v.err(p, "duplicate mark %q", m.Type)
		}
		seen[m.Type] = true
		v.attrs(m.Attrs, spec.Attrs, p)
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func matchChildren(children []*Node, rules []ChildRule) string {
	if len(rules) == 0 {
		if len(children) > 0 {
			return "must not have content"
		}
		return ""
	}
	i := 0
	for _, r := range rules {
		count := 0
		for i < len(children) && (r.Max < 0 || count < r.Max) && children[i] != nil && allowed(r, children[i].Type) {
			i++
			count++
		}
		if count < r.Min {
			return fmt.Sprintf("expected %s at content[%d]", strings.Join(r.Allow, " or "), i)
		}
	}
	if i < len(children) {
		t := "null"
		if children[i] != nil {
			t = children[i].Type
		}
		return fmt.Sprintf("unexpected %s at content[%d]", t, i)
	}
	return ""
}

func allowed(r ChildRule, typ string) bool {
	spec, ok := Nodes[typ]
	return slices.Contains(r.Allow, typ) || (ok && spec.Group != "" && slices.Contains(r.Allow, spec.Group))
}
