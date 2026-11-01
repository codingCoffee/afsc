// Package content holds the ProseMirror document schema, its validator and the HTML renderer.
package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type Node struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []*Node        `json:"content,omitempty"`
	Marks   []*Mark        `json:"marks,omitempty"`
	Text    string         `json:"text,omitempty"`
}

type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Parse decodes a document, rejecting unknown top-level node fields.
func Parse(b []byte) (*Node, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var n Node
	if err := dec.Decode(&n); err != nil {
		return nil, fmt.Errorf("invalid document json: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("invalid document json: trailing data")
	}
	return &n, nil
}

// Walk calls fn for n and every descendant, depth first.
func Walk(n *Node, fn func(*Node)) {
	fn(n)
	for _, c := range n.Content {
		Walk(c, fn)
	}
}

func (n *Node) attr(k string) (any, bool) {
	v, ok := n.Attrs[k]
	return v, ok && v != nil
}

func (n *Node) str(k string) string {
	v, _ := n.attr(k)
	s, _ := v.(string)
	return s
}

func (n *Node) int(k string) (int64, bool) {
	v, ok := n.attr(k)
	if !ok {
		return 0, false
	}
	return toInt(v)
}

func (n *Node) bool(k string) bool {
	v, _ := n.attr(k)
	b, _ := v.(bool)
	return b
}

func (n *Node) classes() []string {
	return strings.Fields(n.str("classes"))
}

func toInt(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
			return 0, false
		}
		return int64(x), true
	case int:
		return int64(x), true
	case int64:
		return x, true
	case json.Number:
		i, err := x.Int64()
		return i, err == nil
	}
	return 0, false
}

// FirstImageID is the media id of the first image node in doc, 0 when there is none.
func FirstImageID(doc *Node) int64 {
	var id int64
	Walk(doc, func(n *Node) {
		if id == 0 && n.Type == "image" {
			id, _ = n.int("mediaId")
		}
	})
	return id
}

// MediaIDs lists the media ids a stored document references, so callers can load them in one query.
func MediaIDs(doc *Node) []int64 {
	var ids []int64
	Walk(doc, func(n *Node) {
		for _, k := range []string{"mediaId", "posterMediaId"} {
			if id, ok := n.int(k); ok {
				ids = append(ids, id)
			}
		}
	})
	return ids
}
