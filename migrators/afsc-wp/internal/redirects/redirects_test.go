package redirects

import (
	"strings"
	"testing"
)

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"/Old-Path":         "/old-path/",
		"old/":              "/old/",
		"/caf%C3%A9?x=1":    "/café/",
		"/files/report.PDF": "/files/report.pdf",
		"/":                 "/",
	}
	for in, want := range cases {
		if got := Canonical(in, "always"); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
	if got := Canonical("/a/", "never"); got != "/a" {
		t.Errorf("never: %q", got)
	}
}

func TestFlatten(t *testing.T) {
	b := NewBuilder("wp", "always")
	b.Add("/a/", "exact", "/b/", 301, "yoast-premium")
	b.Add("/b/", "exact", "/c/", 301, "yoast-premium")
	b.Add("/c", "exact", "/final/", 301, "old-slug")
	b.Add("/A/", "exact", "/other/", 302, "yoast-premium")
	b.Add("/gone/", "exact", "", 410, "yoast-premium")
	b.Add("/to-gone/", "exact", "/gone/", 301, "yoast-premium")
	b.Add("/live/", "exact", "/x/", 301, "old-slug")
	b.Add("/self/", "exact", "/self", 301, "yoast-premium")
	b.Add("^/topics/(.*)$", "regex", "/category/$1/", 301, "yoast-premium")
	out, err := b.Finish(func(p string) bool { return p == "/live/" })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range out {
		got[r.Source] = deref(r.Target)
		if r.Source == "/to-gone/" && r.Status != 410 {
			t.Errorf("chain to a 410 should become 410, got %d", r.Status)
		}
	}
	want := map[string]string{"/a/": "/final/", "/b/": "/final/", "/c/": "/final/", "/gone/": "", "/to-gone/": "", "^/topics/(.*)$": "/category/$1/"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s → %q want %q (all %v)", k, got[k], v, got)
		}
	}
	if _, ok := got["/live/"]; ok || len(b.Shadowed) != 1 {
		t.Errorf("weak redirect over a live path must be dropped")
	}
	if len(b.Conflicts) != 1 || len(b.SelfLoops) != 1 {
		t.Errorf("conflicts %v self %v", b.Conflicts, b.SelfLoops)
	}
}

func TestLoopFails(t *testing.T) {
	b := NewBuilder("wp", "always")
	b.Add("/a/", "exact", "/b/", 301, "x")
	b.Add("/b/", "exact", "/a/", 301, "x")
	if _, err := b.Finish(func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("want loop error, got %v", err)
	}
}

func TestConvertRegex(t *testing.T) {
	ok := map[string]string{
		"^blog/(.*)$":     "^/blog/(.*)$",
		"^/news/([0-9]+)": "^/news/([0-9]+)",
		"old-(\\d+)":      "/old-(\\d+)",
	}
	for in, want := range ok {
		if got, err := ConvertRegex(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{`^(?!wp-admin)(.*)$`, `^(a)\1$`, `^x(?<=y)`, `^a++$`, `^end\Z`} {
		if _, err := ConvertRegex(bad); err == nil {
			t.Errorf("%q should not convert", bad)
		}
	}
}

func TestYoastFormat(t *testing.T) {
	m, s, _ := YoastFormat("old-page", "plain")
	if m != "exact" || s != "/old-page" {
		t.Errorf("plain: %s %s", m, s)
	}
	m, s, _ = YoastFormat("/archive/*", "plain")
	if m != "prefix" || s != "/archive/" {
		t.Errorf("wildcard: %s %s", m, s)
	}
}
