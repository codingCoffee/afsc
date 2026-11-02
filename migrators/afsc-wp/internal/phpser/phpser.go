// Package phpser decodes the subset of PHP serialize() output WordPress stores: null, bools, ints, floats,
// byte-length strings and arrays. Objects and references are errors.
package phpser

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Array is a PHP array with its key order kept. Keys are int64 or string.
type Array struct {
	Keys   []any
	Values []any
}

// Get returns the value under key, matching int keys by their decimal form.
func (a *Array) Get(key string) (any, bool) {
	if a == nil {
		return nil, false
	}
	for i, k := range a.Keys {
		if keyString(k) == key {
			return a.Values[i], true
		}
	}
	return nil, false
}

func keyString(k any) string {
	if i, ok := k.(int64); ok {
		return strconv.FormatInt(i, 10)
	}
	return k.(string)
}

// IsList reports whether the keys are exactly 0..n-1 in order.
func (a *Array) IsList() bool {
	for i, k := range a.Keys {
		if n, ok := k.(int64); !ok || n != int64(i) {
			return false
		}
	}
	return true
}

var ErrTrailing = errors.New("phpser: trailing data")

// Unserialize decodes one value and rejects trailing bytes.
func Unserialize(b []byte) (any, error) {
	d := &decoder{b: b}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if d.i != len(d.b) {
		return nil, ErrTrailing
	}
	return v, nil
}

// LooksSerialized is a cheap check, like WordPress's is_serialized().
func LooksSerialized(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[1] != ':' && s != "N;" {
		return false
	}
	return strings.ContainsRune("Nbidsa", rune(s[0])) && (strings.HasSuffix(s, ";") || strings.HasSuffix(s, "}"))
}

const maxDepth = 64

type decoder struct {
	b []byte
	i int
}

func (d *decoder) fail(format string, args ...any) error {
	return fmt.Errorf("phpser: at byte %d: %s", d.i, fmt.Sprintf(format, args...))
}

func (d *decoder) expect(c byte) error {
	if d.i >= len(d.b) || d.b[d.i] != c {
		return d.fail("expected %q", c)
	}
	d.i++
	return nil
}

// until returns the bytes up to (not including) stop and moves past stop.
func (d *decoder) until(stop byte) (string, error) {
	start := d.i
	for d.i < len(d.b) && d.b[d.i] != stop {
		d.i++
	}
	if d.i >= len(d.b) {
		return "", d.fail("expected %q", stop)
	}
	s := string(d.b[start:d.i])
	d.i++
	return s, nil
}

func (d *decoder) int(stop byte) (int64, error) {
	s, err := d.until(stop)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, d.fail("bad integer %q", s)
	}
	return n, nil
}

func (d *decoder) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, d.fail("nesting too deep")
	}
	if d.i+1 >= len(d.b) {
		return nil, d.fail("unexpected end")
	}
	t := d.b[d.i]
	if t == 'N' {
		d.i++
		return nil, d.expect(';')
	}
	d.i++
	if err := d.expect(':'); err != nil {
		return nil, err
	}
	switch t {
	case 'b':
		n, err := d.int(';')
		if err != nil || (n != 0 && n != 1) {
			return nil, d.fail("bad bool")
		}
		return n == 1, nil
	case 'i':
		return d.int(';')
	case 'd':
		s, err := d.until(';')
		if err != nil {
			return nil, err
		}
		switch s {
		case "INF":
			return math.Inf(1), nil
		case "-INF":
			return math.Inf(-1), nil
		case "NAN":
			return math.NaN(), nil
		}
		f, perr := strconv.ParseFloat(s, 64)
		if perr != nil {
			return nil, d.fail("bad float %q", s)
		}
		return f, nil
	case 's':
		n, err := d.int(':')
		if err != nil {
			return nil, err
		}
		if n < 0 || int64(len(d.b)-d.i) < n+3 {
			return nil, d.fail("string length %d out of range", n)
		}
		if err := d.expect('"'); err != nil {
			return nil, err
		}
		s := string(d.b[d.i : d.i+int(n)])
		d.i += int(n)
		if err := d.expect('"'); err != nil {
			return nil, err
		}
		return s, d.expect(';')
	case 'a':
		n, err := d.int(':')
		if err != nil {
			return nil, err
		}
		if n < 0 || n > int64(len(d.b)) {
			return nil, d.fail("array length %d out of range", n)
		}
		if err := d.expect('{'); err != nil {
			return nil, err
		}
		arr := &Array{}
		for range n {
			k, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			switch k.(type) {
			case int64, string:
			default:
				return nil, d.fail("array key must be int or string")
			}
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			arr.Keys, arr.Values = append(arr.Keys, k), append(arr.Values, v)
		}
		return arr, d.expect('}')
	case 'O', 'C', 'E':
		return nil, d.fail("objects are not supported")
	case 'r', 'R':
		return nil, d.fail("references are not supported")
	}
	return nil, d.fail("unknown type %q", t)
}

// Plain converts a decoded value to JSON-friendly Go values: lists become []any, other arrays map[string]any.
func Plain(v any) any {
	a, ok := v.(*Array)
	if !ok {
		if f, isF := v.(float64); isF && (math.IsInf(f, 0) || math.IsNaN(f)) {
			return nil
		}
		return v
	}
	if a.IsList() {
		out := make([]any, len(a.Values))
		for i, x := range a.Values {
			out[i] = Plain(x)
		}
		return out
	}
	out := make(map[string]any, len(a.Keys))
	for i, k := range a.Keys {
		out[keyString(k)] = Plain(a.Values[i])
	}
	return out
}

// Decode unserializes s when it looks serialized; otherwise it returns s unchanged.
func Decode(s string) (any, error) {
	if !LooksSerialized(s) {
		return s, nil
	}
	v, err := Unserialize([]byte(s))
	if err != nil {
		return nil, err
	}
	return Plain(v), nil
}
