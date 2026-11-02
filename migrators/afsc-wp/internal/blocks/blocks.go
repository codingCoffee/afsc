// Package blocks parses block-editor delimiters (<!-- wp:name {json} -->…<!-- /wp:name -->) into a tree.
package blocks

import (
	"encoding/json"
	"regexp"
	"strings"
)

type Block struct {
	Name  string // "core/paragraph"; empty for freeform HTML
	Attrs map[string]any
	Parts []Part // inner HTML and inner blocks, in document order
}

// Part is either HTML or a nested block.
type Part struct {
	HTML  string
	Block *Block
}

// InnerHTML is the block's own markup with inner blocks left out.
func (b *Block) InnerHTML() string {
	var s strings.Builder
	for _, p := range b.Parts {
		if p.Block == nil {
			s.WriteString(p.HTML)
		}
	}
	return s.String()
}

// Inner returns the nested blocks.
func (b *Block) Inner() []*Block {
	var out []*Block
	for _, p := range b.Parts {
		if p.Block != nil {
			out = append(out, p.Block)
		}
	}
	return out
}

var delimRe = regexp.MustCompile(`(?s)<!--\s+(/)?wp:([a-z][a-z0-9_-]*/)?([a-z][a-z0-9_-]*)\s+(\{.*?\}\s+)?(/)?-->`)

// HasBlocks reports whether s contains block delimiters, like WordPress's has_blocks().
func HasBlocks(s string) bool { return strings.Contains(s, "<!-- wp:") }

// Parse returns the top-level parts. Unclosed blocks are closed at the end, as WordPress's parser does.
func Parse(s string) []Part {
	root := &Block{}
	stack := []*Block{root}
	top := func() *Block { return stack[len(stack)-1] }
	addHTML := func(h string) {
		if h != "" {
			top().Parts = append(top().Parts, Part{HTML: h})
		}
	}
	last := 0
	for _, m := range delimRe.FindAllStringSubmatchIndex(s, -1) {
		addHTML(s[last:m[0]])
		last = m[1]
		closing := m[2] >= 0
		ns := "core/"
		if m[4] >= 0 {
			ns = s[m[4]:m[5]]
		}
		name := ns + s[m[6]:m[7]]
		if closing {
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].Name == name {
					stack = stack[:i]
					break
				}
			}
			continue
		}
		b := &Block{Name: name, Attrs: map[string]any{}}
		if m[8] >= 0 {
			if err := json.Unmarshal([]byte(strings.TrimSpace(s[m[8]:m[9]])), &b.Attrs); err != nil {
				b.Attrs = map[string]any{"_invalid_json": s[m[8]:m[9]]}
			}
		}
		top().Parts = append(top().Parts, Part{Block: b})
		if m[10] < 0 {
			stack = append(stack, b)
		}
	}
	addHTML(s[last:])
	return root.Parts
}

// Names counts block names in s, including nested ones.
func Names(s string) map[string]int {
	out := map[string]int{}
	var walk func(parts []Part)
	walk = func(parts []Part) {
		for _, p := range parts {
			if p.Block != nil {
				out[p.Block.Name]++
				walk(p.Block.Parts)
			}
		}
	}
	walk(Parse(s))
	return out
}
