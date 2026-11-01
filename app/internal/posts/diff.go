package posts

import (
	"encoding/json"
	"unicode"

	"afsc/internal/content"
)

type DiffOp struct {
	Op   string `json:"op"` // equal, insert, delete
	Text string `json:"text"`
}

type BlockChange struct {
	Index  int    `json:"index"`
	Change string `json:"change"` // added, removed, changed
	Type   string `json:"type"`
}

type RevisionDiff struct {
	Title  []DiffOp      `json:"title"`
	Text   []DiffOp      `json:"text"`
	Blocks []BlockChange `json:"blocks"`
}

type editOp byte

const (
	opEqual editOp = iota
	opDelete
	opInsert
)

// maxEdits bounds Myers' search; beyond it the middle is reported as one delete and one insert.
const maxEdits = 1000

// script is a shortest edit script from a to b (Myers, after trimming the common prefix and suffix). It is
// deterministic: the same inputs always give the same script.
func script[T comparable](a, b []T) []editOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	out := make([]editOp, 0, len(a)+len(b))
	for range pre {
		out = append(out, opEqual)
	}
	out = append(out, myers(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for range suf {
		out = append(out, opEqual)
	}
	return out
}

func myers[T comparable](a, b []T) []editOp {
	n, m := len(a), len(b)
	coarse := func() []editOp {
		out := make([]editOp, 0, n+m)
		for range n {
			out = append(out, opDelete)
		}
		for range m {
			out = append(out, opInsert)
		}
		return out
	}
	if n == 0 || m == 0 {
		return coarse()
	}
	maxD := min(n+m, maxEdits)
	off := maxD + 1
	v := make([]int, 2*maxD+3)
	// trace[d] holds v[-d..d] as it was before step d
	var trace [][]int
	for d := 0; d <= maxD; d++ {
		trace = append(trace, append([]int(nil), v[off-d:off+d+1]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m)
			}
		}
	}
	return coarse()
}

func backtrack(trace [][]int, x, y int) []editOp {
	var rev []editOp
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		at := func(k int) int { return v[k+d] }
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := 0
		if d > 0 {
			prevX = at(prevK)
		}
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			rev = append(rev, opEqual)
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == prevX {
				rev = append(rev, opInsert)
			} else {
				rev = append(rev, opDelete)
			}
		}
		x, y = prevX, prevY
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// words splits s into alternating runs of whitespace and non-whitespace, so joining them gives s back.
func words(s string) []string {
	var out []string
	start, space := 0, false
	for i, r := range s {
		sp := unicode.IsSpace(r)
		if i > start && sp != space {
			out = append(out, s[start:i])
			start = i
		}
		space = sp
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// WordDiff is a word-level diff from a to b with adjacent operations of the same kind merged.
func WordDiff(a, b string) []DiffOp {
	wa, wb := words(a), words(b)
	out := []DiffOp{}
	i, j := 0, 0
	for _, op := range script(wa, wb) {
		var name, text string
		switch op {
		case opEqual:
			name, text = "equal", wa[i]
			i, j = i+1, j+1
		case opDelete:
			name, text = "delete", wa[i]
			i++
		case opInsert:
			name, text = "insert", wb[j]
			j++
		}
		if n := len(out); n > 0 && out[n-1].Op == name {
			out[n-1].Text += text
		} else {
			out = append(out, DiffOp{Op: name, Text: text})
		}
	}
	return out
}

// BlockDiff summarises top-level block changes. A removed block followed by an added one of the same type is
// "changed". Indexes are into b for added and changed blocks and into a for removed ones.
func BlockDiff(a, b *content.Node) []BlockChange {
	key := func(n *content.Node) []string {
		var out []string
		if n == nil {
			return out
		}
		for _, c := range n.Content {
			j, _ := json.Marshal(c)
			out = append(out, string(j))
		}
		return out
	}
	typ := func(n *content.Node, i int) string { return n.Content[i].Type }
	ka, kb := key(a), key(b)
	out := []BlockChange{}
	i, j := 0, 0
	var removed []int
	flush := func() {
		for _, r := range removed {
			out = append(out, BlockChange{Index: r, Change: "removed", Type: typ(a, r)})
		}
		removed = nil
	}
	for _, op := range script(ka, kb) {
		switch op {
		case opEqual:
			flush()
			i, j = i+1, j+1
		case opDelete:
			removed = append(removed, i)
			i++
		case opInsert:
			if len(removed) > 0 && typ(a, removed[0]) == typ(b, j) {
				out = append(out, BlockChange{Index: j, Change: "changed", Type: typ(b, j)})
				removed = removed[1:]
			} else {
				out = append(out, BlockChange{Index: j, Change: "added", Type: typ(b, j)})
			}
			j++
		}
	}
	flush()
	return out
}
