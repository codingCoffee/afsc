package phpser

import (
	"reflect"
	"testing"
)

func TestUnserializePlain(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{`N;`, nil},
		{`b:1;`, true},
		{`b:0;`, false},
		{`i:-42;`, int64(-42)},
		{`d:1.5;`, 1.5},
		{`s:5:"hello";`, "hello"},
		{`s:5:"café";`, "café"},
		{`s:7:"a"b;c"d";`, `a"b;c"d`},
		{`s:0:"";`, ""},
		{`a:2:{i:0;s:1:"a";i:1;s:1:"b";}`, []any{"a", "b"}},
		{`a:2:{s:5:"width";i:300;s:6:"height";i:200;}`, map[string]any{"width": int64(300), "height": int64(200)}},
		{`a:1:{s:4:"list";a:1:{i:5;b:1;}}`, map[string]any{"list": map[string]any{"5": true}}},
		{`a:0:{}`, []any{}},
	}
	for _, c := range cases {
		v, err := Unserialize([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got := Plain(v); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestByteLengthStrings(t *testing.T) {
	// "₹" is three bytes; a character-counting reader would read past the closing quote.
	v, err := Unserialize([]byte(`a:1:{s:3:"cur";s:4:"₹1";}`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := v.(*Array).Get("cur")
	if got != "₹1" {
		t.Fatalf("got %q", got)
	}
}

func TestKeyOrderKept(t *testing.T) {
	v, err := Unserialize([]byte(`a:3:{s:1:"z";i:1;s:1:"a";i:2;i:7;i:3;}`))
	if err != nil {
		t.Fatal(err)
	}
	a := v.(*Array)
	want := []any{"z", "a", int64(7)}
	if !reflect.DeepEqual(a.Keys, want) {
		t.Fatalf("keys %v", a.Keys)
	}
	if x, _ := a.Get("7"); x != int64(3) {
		t.Fatalf("int key lookup: %v", x)
	}
}

func TestErrors(t *testing.T) {
	bad := []string{
		`O:8:"stdClass":0:{}`,
		`a:1:{i:0;O:3:"Foo":0:{}}`,
		`C:3:"Foo":0:{}`,
		`r:1;`,
		`s:10:"short";`,
		`s:-1:"";`,
		`i:abc;`,
		`b:2;`,
		`a:2:{i:0;s:1:"a";}`,
		`a:1:{a:0:{}i:1;}`,
		`s:1:"a";extra`,
		`x:1;`,
		``,
	}
	for _, in := range bad {
		if _, err := Unserialize([]byte(in)); err == nil {
			t.Errorf("%q: want error", in)
		}
	}
}

func TestDecode(t *testing.T) {
	if v, _ := Decode("plain text"); v != "plain text" {
		t.Fatalf("plain: %v", v)
	}
	v, err := Decode(`a:1:{i:0;s:4:"post";}`)
	if err != nil || !reflect.DeepEqual(v, []any{"post"}) {
		t.Fatalf("got %v %v", v, err)
	}
}

func FuzzUnserialize(f *testing.F) {
	for _, s := range []string{`N;`, `b:1;`, `i:5;`, `d:0.5;`, `s:3:"abc";`, `a:1:{i:0;a:1:{s:1:"k";s:1:"v";}}`, `O:1:"a":0:{}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := Unserialize(b)
		if err != nil {
			return
		}
		_ = Plain(v)
	})
}
