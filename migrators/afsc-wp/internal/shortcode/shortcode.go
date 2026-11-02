// Package shortcode parses WordPress shortcodes with WordPress's own semantics (get_shortcode_regex,
// shortcode_parse_atts): only registered tags match, [[tag]] escapes, the first [/tag] closes an enclosing tag.
package shortcode

import (
	"regexp"
	"sort"
	"strings"
)

type Shortcode struct {
	Tag       string
	Attrs     map[string]string
	Pos       []string
	Content   string
	Enclosing bool
	Raw       string
}

// Attr returns a named attribute, falling back to a positional "=value" form (Jetpack's [youtube=URL]).
func (s *Shortcode) Attr(name string) string {
	return s.Attrs[name]
}

// First returns the first positional value with a leading "=" removed.
func (s *Shortcode) First() string {
	if len(s.Pos) == 0 {
		return ""
	}
	return strings.TrimPrefix(s.Pos[0], "=")
}

// Segment is either literal text or one shortcode.
type Segment struct {
	Text string
	SC   *Shortcode
}

func isTagChar(c byte) bool {
	return c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Parse splits s into text and shortcodes for the given tags. Text inside HTML tags is never parsed.
func Parse(s string, tags map[string]bool) []Segment {
	var out []Segment
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, Segment{Text: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c == '<' {
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				text.WriteString(s[i:])
				break
			}
			text.WriteString(s[i : i+end+1])
			i += end + 1
			continue
		}
		if c != '[' {
			text.WriteByte(c)
			i++
			continue
		}
		sc, end, escaped := match(s, i, tags)
		switch {
		case sc == nil:
			text.WriteByte(c)
			i++
		case escaped:
			text.WriteString(s[i+1 : end-1])
			i = end
		default:
			if s[i+1] == '[' {
				text.WriteByte('[')
			}
			flush()
			out = append(out, Segment{SC: sc})
			i = end
		}
	}
	flush()
	return out
}

// match tries a shortcode at s[i] == '['. end is the index after the match (including a trailing "]" when escaped).
func match(s string, i int, tags map[string]bool) (*Shortcode, int, bool) {
	j := i + 1
	esc := j < len(s) && s[j] == '['
	if esc {
		j++
	}
	start := j
	for j < len(s) && isTagChar(s[j]) {
		j++
	}
	tag := s[start:j]
	if tag == "" || !tags[tag] || j >= len(s) {
		return nil, 0, false
	}
	attrEnd := strings.IndexByte(s[j:], ']')
	if attrEnd < 0 {
		return nil, 0, false
	}
	attrText := s[j : j+attrEnd]
	selfClosing := strings.HasSuffix(attrText, "/")
	if selfClosing {
		attrText = attrText[:len(attrText)-1]
	}
	after := j + attrEnd + 1
	sc := &Shortcode{Tag: tag}
	sc.Attrs, sc.Pos = ParseAttrs(attrText)
	end := after
	if !selfClosing {
		closeTag := "[/" + tag + "]"
		if k := strings.Index(s[after:], closeTag); k >= 0 {
			sc.Content, sc.Enclosing = s[after:after+k], true
			end = after + k + len(closeTag)
		}
	}
	sc.Raw = s[start-1 : end]
	if esc && end < len(s) && s[end] == ']' {
		return sc, end + 1, true
	}
	return sc, end, false
}

var attrRe = regexp.MustCompile(`([\w-]+)\s*=\s*"([^"]*)"(?:\s|$)|([\w-]+)\s*=\s*'([^']*)'(?:\s|$)|([\w-]+)\s*=\s*([^\s'"]+)(?:\s|$)|"([^"]*)"(?:\s|$)|'([^']*)'(?:\s|$)|(\S+)(?:\s|$)`)

var spaceRe = regexp.MustCompile("[\u00a0\u200b]+")

// ParseAttrs mirrors shortcode_parse_atts: named attributes lowercased, the rest positional.
func ParseAttrs(text string) (map[string]string, []string) {
	named := map[string]string{}
	var pos []string
	text = spaceRe.ReplaceAllString(text, " ")
	for _, m := range attrRe.FindAllStringSubmatch(text, -1) {
		switch {
		case m[1] != "":
			named[strings.ToLower(m[1])] = stripCSlashes(m[2])
		case m[3] != "":
			named[strings.ToLower(m[3])] = stripCSlashes(m[4])
		case m[5] != "":
			named[strings.ToLower(m[5])] = stripCSlashes(m[6])
		case m[7] != "" || strings.HasPrefix(m[0], `"`):
			pos = append(pos, stripCSlashes(m[7]))
		case m[8] != "" || strings.HasPrefix(m[0], `'`):
			pos = append(pos, stripCSlashes(m[8]))
		default:
			pos = append(pos, stripCSlashes(m[9]))
		}
	}
	return named, pos
}

func stripCSlashes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

var candidateRe = regexp.MustCompile(`\[\[?([A-Za-z0-9_][A-Za-z0-9_-]*)`)

// Occurrence is one candidate shortcode seen by the inventory scan.
type Occurrence struct {
	Tag       string
	AttrNames []string
	Enclosing bool
}

// Scan finds every [name …] candidate in s, treating every name as registered.
func Scan(s string) []Occurrence {
	tags := map[string]bool{}
	for _, m := range candidateRe.FindAllStringSubmatch(s, -1) {
		tags[m[1]] = true
	}
	if len(tags) == 0 {
		return nil
	}
	var out []Occurrence
	var walk func(segs []Segment)
	walk = func(segs []Segment) {
		for _, seg := range segs {
			if seg.SC == nil {
				continue
			}
			o := Occurrence{Tag: seg.SC.Tag, Enclosing: seg.SC.Enclosing}
			for k := range seg.SC.Attrs {
				o.AttrNames = append(o.AttrNames, k)
			}
			sort.Strings(o.AttrNames)
			out = append(out, o)
			if seg.SC.Content != "" {
				walk(Parse(seg.SC.Content, tags))
			}
		}
	}
	walk(Parse(s, tags))
	return out
}

// InTagCount counts shortcode-looking text inside HTML tag attributes, which afsc-wp never converts.
func InTagCount(s string) int {
	n := 0
	for i := 0; i < len(s); {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			break
		}
		gt := strings.IndexByte(s[i+lt:], '>')
		if gt < 0 {
			break
		}
		n += len(candidateRe.FindAllStringIndex(s[i+lt:i+lt+gt], -1))
		i += lt + gt + 1
	}
	return n
}
