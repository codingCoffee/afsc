package redirects

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fakeRouter is a tiny site: "always" trailing slashes, a few pages and SEO files, one content redirect.
type fakeRouter struct {
	pages map[string]bool
	moved map[string]string
}

func (fakeRouter) BaseURL() string { return "https://blog.example.com" }

func (fakeRouter) Canonical(p string) string {
	c := multiSlash.ReplaceAllString(p, "/")
	last := c[strings.LastIndexByte(c, '/')+1:]
	if strings.Contains(last, ".") {
		return c
	}
	c = strings.ToLower(c)
	if !strings.HasSuffix(c, "/") {
		c += "/"
	}
	return c
}

func (fakeRouter) Before(_ context.Context, path, _ string) (Outcome, error) {
	switch {
	case reserved(path):
		return Outcome{Status: 404}, nil
	case path == "/robots.txt":
		return Outcome{Status: 200}, nil
	case path == "/sitemap.xml":
		return Outcome{Status: 301, Location: "/sitemap_index.xml"}, nil
	}
	return Outcome{}, nil
}

func (f fakeRouter) Content(_ context.Context, path string) (Outcome, error) {
	if to, ok := f.moved[path]; ok {
		return Outcome{Status: 301, Location: to}, nil
	}
	if f.pages[path] {
		return Outcome{Status: 200}, nil
	}
	return Outcome{}, nil
}

var router = fakeRouter{
	pages: map[string]bool{"/live/": true, "/c/": true, "/old/kept/": true, "/category/news/": true, "/p1/": true, "/p2/": true,
		"/n/kept/": true},
	moved: map[string]string{"/home-moved/": "/live/"},
}

func rule(id int64, match, source, target string, status int) *Rule {
	r := &Rule{ID: id, Match: match, Source: source, Target: target, Status: status}
	if err := r.Compile(); err != nil {
		panic(err)
	}
	return r
}

func baseRules() []*Rule {
	return []*Rule{
		rule(1, "exact", "/a/", "/b/", 301),
		rule(2, "exact", "/b/", "/c/", 301),
		rule(3, "exact", "/gone/", "", 410),
		rule(4, "prefix", "/old/", "/live/", 301),
		rule(5, "regex", `^/topics/([a-z]+)/$`, "/category/$1/", 301),
		rule(6, "exact", "/x/", "/y/", 301),
		rule(7, "exact", "/y/", "/x/", 301),
		rule(8, "exact", "/p/", "/n/", 301),
	}
}

func newBatch(rules []*Rule) *batch {
	return &batch{ctx: context.Background(), rt: router, t: NewTable(router, rules), orig: map[*Rule]Rule{}}
}

func TestCheckChainsAndLoops(t *testing.T) {
	type want struct {
		code, flat string
		hops       int
		warn       string
	}
	cases := []struct {
		match, source, target string
		status                int
		want                  want
	}{
		{"exact", "/n/", "/live/", 301, want{}},
		{"exact", "/n/", "/a/", 301, want{code: "chain", flat: "/c/", hops: 2}},
		{"exact", "/n/", "https://blog.example.com/a/", 301, want{code: "chain", flat: "/c/", hops: 2}},
		{"exact", "/n/", "/topics/news/", 301, want{code: "chain", flat: "/category/news/", hops: 1}},
		{"exact", "/n/", "/old/thing/", 301, want{code: "chain", flat: "/live/", hops: 1}},
		{"exact", "/n/", "/old/kept/", 301, want{}},
		{"exact", "/n/", "/home-moved/", 301, want{code: "chain", flat: "/live/", hops: 1}},
		{"exact", "/n/", "/sitemap.xml", 301, want{code: "chain", flat: "/sitemap_index.xml", hops: 1}},
		{"exact", "/n/", "/Live", 301, want{code: "chain", flat: "/live/", hops: 1}},
		{"exact", "/n/", "/gone/", 301, want{code: "gone", hops: 1}},
		{"exact", "/n/", "/x/", 301, want{code: "loop", hops: 2}},
		{"exact", "/n/", "/p/", 301, want{code: "loop", hops: 2}},
		{"prefix", "/n/", "/n/sub/", 301, want{code: "loop", hops: 1}},
		{"prefix", "/n/", "/n/kept/", 301, want{}},
		{"exact", "/n/", "https://other.example/x", 301, want{}},
		{"exact", "/n/", "/missing/", 301, want{warn: "404"}},
		{"exact", "/live/", "/c/", 301, want{warn: "live page"}},
		{"regex", `^/q/(.*)$`, "/x/$1", 301, want{warn: "aren't checked"}},
		{"exact", "/n/", "", 410, want{}},
	}
	for _, c := range cases {
		name := fmt.Sprintf("%s %s -> %s", c.match, c.source, c.target)
		b := newBatch(baseRules())
		r := rule(0, c.match, c.source, c.target, c.status)
		b.create(r)
		warns, err := check(b.ctx, router, b.t, r)
		var ce *ChainError
		switch {
		case c.want.code == "" && err != nil:
			t.Errorf("%s: unexpected %v", name, err)
		case c.want.code != "" && !errors.As(err, &ce):
			t.Errorf("%s: err = %v, want %s", name, err, c.want.code)
		case ce != nil && (ce.Code != c.want.code || ce.Flattened != c.want.flat || len(ce.Hops) != c.want.hops):
			t.Errorf("%s: got %s %q %d hops %+v, want %+v", name, ce.Code, ce.Flattened, len(ce.Hops), ce.Hops, c.want)
		}
		if c.want.warn != "" && !slices.ContainsFunc(warns, func(w string) bool { return strings.Contains(w, c.want.warn) }) {
			t.Errorf("%s: warnings %q, want one with %q", name, warns, c.want.warn)
		}
	}
}

func TestChainTooLong(t *testing.T) {
	var rules []*Rule
	for i := range MaxHops + 1 {
		rules = append(rules, rule(int64(i+1), "exact", fmt.Sprintf("/h%d/", i), fmt.Sprintf("/h%d/", i+1), 301))
	}
	b := newBatch(rules)
	r := rule(0, "exact", "/start/", "/h0/", 301)
	b.create(r)
	_, err := check(b.ctx, router, b.t, r)
	var ce *ChainError
	if !errors.As(err, &ce) || ce.Code != "too_long" || ce.Flattened != "" {
		t.Fatalf("err = %v", err)
	}
}

func TestPutFlattens(t *testing.T) {
	b := newBatch(baseRules())
	if _, _, _, err := b.put(nil, rule(0, "exact", "/n/", "/a/", 301), false); err == nil {
		t.Fatal("a chain must be rejected without flatten")
	}
	if b.t.exact["/n/"] != nil || len(b.created) != 0 {
		t.Fatal("a rejected put must leave the table as it was")
	}
	r, _, _, err := b.put(nil, rule(0, "exact", "/n/", "/a/", 301), true)
	if err != nil || r.Target != "/c/" {
		t.Fatalf("flatten: %+v %v", r, err)
	}

	// /b/, /n/ and /p/ now point at /c/; making /c/ redirect points them straight at its target
	r, warns, flat, err := b.put(nil, rule(0, "exact", "/c/", "/live/", 301), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"/b/", "/n/", "/p/"} {
		if b.t.exact[src].Target != "/live/" {
			t.Errorf("%s -> %q", src, b.t.exact[src].Target)
		}
	}
	if len(flat) != 3 || len(warns) != 1 || r.Target != "/live/" {
		t.Errorf("flattened %d, warns %q", len(flat), warns)
	}
	if p := b.purge(); p.Refresh || !slices.Equal(p.Paths, []string{"/b/", "/c/", "/n/", "/p/"}) {
		t.Errorf("purge = %+v", p)
	}

	// /x/ <-> /y/ was a loop; fixing /x/ flattens /y/, and editing /y/ back onto itself is rejected and undone
	if _, _, _, err := b.put(b.t.byID[6], rule(0, "exact", "/x/", "/live/", 301), false); err != nil {
		t.Fatalf("x -> live: %v", err)
	}
	if b.t.byID[7].Target != "/live/" {
		t.Errorf("/y/ not flattened: %+v", b.t.byID[7])
	}
	if _, _, _, err := b.put(b.t.byID[7], rule(0, "exact", "/y/", "/x/", 301), false); err == nil {
		t.Fatal("/y/ -> /x/ is a chain and must fail")
	}
	if b.t.byID[7].Target != "/live/" || b.t.exact["/y/"] != b.t.byID[7] {
		t.Errorf("rejected edit kept: %+v", b.t.byID[7])
	}
}

func TestPrefixIncomingFlatten(t *testing.T) {
	b := newBatch(append(baseRules(), rule(9, "exact", "/z/", "/bz/thing/", 301)))
	if _, _, flat, err := b.put(nil, rule(0, "prefix", "/bz/", "/live/", 301), false); err != nil || len(flat) != 1 || flat[0].ID != 9 {
		t.Fatalf("flat %v err %v", flat, err)
	}
	if b.t.byID[9].Target != "/live/" || !b.purge().Refresh {
		t.Errorf("/z/ -> %q, purge %+v", b.t.byID[9].Target, b.purge())
	}
}

func TestSlugChange(t *testing.T) {
	b := newBatch([]*Rule{rule(1, "exact", "/older/", "/p1/?ref=x", 301), rule(2, "exact", "/p2/", "/elsewhere/", 302)})
	created, _, flat := b.slugChange([]Move{{Old: "/p1/", New: "/p2/"}})
	if len(created) != 1 || created[0].Source != "/p1/" || created[0].Target != "/p2/" || len(flat) != 1 {
		t.Fatalf("created %+v flat %+v", created, flat)
	}
	if b.t.byID[1].Target != "/p2/?ref=x" || b.t.byID[2] != nil {
		t.Errorf("older -> %q; /p2/ rule must be deleted: %+v", b.t.byID[1].Target, b.t.byID[2])
	}
	// renaming back drops the first auto redirect rather than leaving /p1/ -> /p1/
	b.slugChange([]Move{{Old: "/p2/", New: "/p1/"}})
	if b.t.exact["/p1/"] != nil || b.t.exact["/p2/"].Target != "/p1/" || b.t.byID[1].Target != "/p1/?ref=x" {
		t.Errorf("after rename back: p1 %+v p2 %+v older %+v", b.t.exact["/p1/"], b.t.exact["/p2/"], b.t.byID[1])
	}
	if len(b.created) != 1 || len(b.deleted) != 1 || b.deleted[0].ID != 2 {
		t.Errorf("created %d deleted %+v", len(b.created), b.deleted)
	}
}

func TestMoves(t *testing.T) {
	got := Moves(map[string]string{"post:1": "/a/", "post:2": "/b/", "post:3": "/c/"},
		map[string]string{"post:1": "/a2/", "post:2": "/b/"})
	if !slices.Equal(got, []Move{{Old: "/a/", New: "/a2/"}}) {
		t.Errorf("Moves = %+v", got)
	}
}

func TestNormalize(t *testing.T) {
	str := func(s string) *string { return &s }
	ok := []struct {
		in             Input
		source, target string
		status         int
	}{
		{Input{Source: "/Old-Path", Target: str("/New-Path")}, "/old-path/", "/new-path/", 301},
		{Input{Source: "//a//b/", Target: str("https://BLOG.example.com//x?y=1#z")}, "/a/b/", "/x/?y=1#z", 301},
		{Input{Source: "/caf%C3%A9/", Target: str("/thé/")}, "/café/", "/th%C3%A9/", 301},
		{Input{Source: "/File.PDF", Target: str("https://other.example/Doc")}, "/File.PDF", "https://other.example/Doc", 301},
		{Input{Source: "/Old", Match: "prefix", Target: str("/")}, "/old", "/", 301},
		{Input{Source: `^/t/(\d+)$`, Match: "regex", Target: str("/p/$1"), Status: 308}, `^/t/(\d+)$`, "/p/$1", 308},
		{Input{Source: "/gone/", Status: 451}, "/gone/", "", 451},
	}
	for _, c := range ok {
		r, err := Normalize(c.in, router)
		if err != nil || r.Source != c.source || r.Target != c.target || r.Status != c.status {
			t.Errorf("Normalize(%+v) = %+v, %v", c.in, r, err)
		}
	}
	bad := []struct {
		in    Input
		field string
	}{
		{Input{Source: "/a/", Target: str("/b/"), Status: 303}, "status"},
		{Input{Source: "/a/", Target: str("/b/"), Status: 410}, "target"},
		{Input{Source: "/a/"}, "target"},
		{Input{Source: "a/", Target: str("/b/")}, "source"},
		{Input{Source: "/a/?x=1", Target: str("/b/")}, "source"},
		{Input{Source: "/admin/x", Target: str("/b/")}, "source"},
		{Input{Source: "^/admin", Match: "regex", Target: str("/b/")}, "source"},
		{Input{Source: "/a/", Match: "glob", Target: str("/b/")}, "match"},
		{Input{Source: "/a", Target: str("/A/")}, "target"},
		{Input{Source: "/a/", Target: str("b/")}, "target"},
		{Input{Source: "/a/", Target: str("//evil.example/")}, "target"},
		{Input{Source: "/a/", Target: str("javascript:alert(1)")}, "target"},
		{Input{Source: "/a/", Target: str("/b/\r\nX: y")}, "target"},
		{Input{Source: "(?<=x)a", Match: "regex", Target: str("/b/")}, "source"},
		{Input{Source: "(?:abcdefghij){200}", Match: "regex", Target: str("/b/")}, "source"},
		{Input{Source: "/" + strings.Repeat("a", MaxRegex), Match: "regex", Target: str("/b/")}, "source"},
	}
	for _, c := range bad {
		_, err := Normalize(c.in, router)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("Normalize(%+v) = %v, want a %s error", c.in, err, c.field)
		}
	}
}

func TestTester(t *testing.T) {
	res, err := Test("regex", `^/topics/([a-z]+)/$`, "/category/$1/", []string{"/topics/news", "/Topics/News/", "/other/"}, router)
	if err != nil {
		t.Fatal(err)
	}
	want := []TestResult{
		{Path: "/topics/news", Canonical: "/topics/news/", Matched: true, Target: "/category/news/"},
		{Path: "/Topics/News/", Canonical: "/topics/news/", Matched: true, Target: "/category/news/"},
		{Path: "/other/", Canonical: "/other/"},
	}
	if !slices.Equal(res, want) {
		t.Errorf("Test = %+v", res)
	}
	if res, _ := Test("exact", "/Legacy.html", "", []string{"/Legacy.html", "/legacy.html"}, router); !res[0].Matched || res[1].Matched {
		t.Errorf("exact file paths keep their case: %+v", res)
	}
	if _, err := Test("regex", "(", "", nil, router); err == nil {
		t.Error("a bad pattern must fail")
	}
}

func TestCSVRoundTrip(t *testing.T) {
	rules := []*Rule{
		rule(1, "exact", "/a/", "/b/", 301),
		rule(2, "regex", `^/t/(\d+)$`, "/p/$1", 308),
		{ID: 3, Match: "exact", Source: "/gone/", Status: 410, Note: "=HYPERLINK(\"x\"), \"quoted\""},
		{ID: 4, Match: "prefix", Source: "/old", Target: "https://other.example/?a=1,2", Status: 302, Note: "-1"},
	}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, rules); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), ",=HYPERLINK") || strings.Contains(buf.String(), "\n=") {
		t.Errorf("formula cell not defanged:\n%s", buf.String())
	}
	lines, errs, err := ReadCSV(&buf)
	if err != nil || len(errs) > 0 || len(lines) != len(rules) {
		t.Fatalf("ReadCSV: %d lines, %v, %v", len(lines), errs, err)
	}
	for i, l := range lines {
		r := rules[i]
		target := ""
		if l.Input.Target != nil {
			target = *l.Input.Target
		}
		if l.N != i+2 || l.Input.Source != r.Source || l.Input.Match != r.Match || target != r.Target ||
			l.Input.Status != r.Status || l.Input.Note != r.Note {
			t.Errorf("line %d: %+v (target %q), want %+v", l.N, l.Input, target, r)
		}
	}

	_, errs, err = ReadCSV(strings.NewReader("note,source\nhi,/a/\n,\nx,/b/,extra\n"))
	if err != nil || len(errs) != 0 {
		t.Errorf("reordered columns: %v %v", errs, err)
	}
	_, errs, _ = ReadCSV(strings.NewReader("source,status\n/a/,abc\n"))
	if len(errs) != 1 || errs[0].Line != 2 || errs[0].Field != "status" {
		t.Errorf("bad status: %+v", errs)
	}
	for _, in := range []string{"", "origin,target\n", "target\n/b/\n", "source,source\n"} {
		if _, _, err := ReadCSV(strings.NewReader(in)); err == nil {
			t.Errorf("ReadCSV(%q) must fail", in)
		}
	}
}
