package pagecache

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestKey(t *testing.T) {
	long := strings.Repeat("é", MaxQueryRunes+5)
	cases := []struct{ target, key, query string }{
		{"/", "h/", ""},
		{"/about/", "h/about/", ""},
		{"/about/?utm_source=x&fbclid=y", "h/about/", ""},
		{"/search/?q=bonds&page=2", "h/search/?page=2&q=bonds", "page=2&q=bonds"},
		{"/search/?page=2&q=bonds", "h/search/?page=2&q=bonds", "page=2&q=bonds"},
		{"/search/?q=%20bonds%20", "h/search/?q=bonds", "q=bonds"},
		{"/search/?q=", "h/search/", ""},
		{"/x/?page=1", "h/x/", ""},
		{"/x/?page=0", "h/x/", ""},
		{"/x/?page=abc", "h/x/", ""},
		{"/x/?page=02", "h/x/?page=2", "page=2"},
		{"/x/?q=a&q=b", "h/x/?q=a", "q=a"},
		{"/x/?q=" + url.QueryEscape(long), "h/x/?q=" + url.QueryEscape(long[:2*MaxQueryRunes]), "q=" + url.QueryEscape(long[:2*MaxQueryRunes])},
		{"/caf%C3%A9/", "h/café/", ""},
		{"/x/?%zz=1&page=3", "h/x/?page=3", "page=3"},
	}
	for _, c := range cases {
		key, query := Key("h", httptest.NewRequest("GET", c.target, nil).URL)
		if key != c.key || query != c.query {
			t.Errorf("Key(%s) = %q, %q; want %q, %q", c.target, key, query, c.key, c.query)
		}
	}
}

func TestBypass(t *testing.T) {
	req := func(method, target string, mod func(*http.Request)) *http.Request {
		r := httptest.NewRequest(method, target, nil)
		if mod != nil {
			mod(r)
		}
		return r
	}
	cookie := func(name string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: name, Value: "1"}) }
	}
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"plain get", req("GET", "/", nil), false},
		{"head", req("HEAD", "/about/", nil), false},
		{"post", req("POST", "/", nil), true},
		{"authorization", req("GET", "/", func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }), true},
		{"afsc cookie", req("GET", "/", cookie("afsc_preview")), true},
		{"analytics cookie", req("GET", "/", cookie("_ga")), false},
		{"wpcompat p", req("GET", "/?p=5", nil), true},
		{"wpcompat page_id", req("GET", "/?page_id=5", nil), true},
		{"wpcompat s", req("GET", "/?s=x", nil), true},
		{"wpcompat empty feed", req("GET", "/?feed", nil), true},
		{"preview", req("GET", "/x/?preview=1", nil), true},
		{"tracking params", req("GET", "/?utm_source=x", nil), false},
		{"allowed params", req("GET", "/search/?q=x&page=2", nil), false},
	}
	for _, c := range cases {
		if got := Bypass(c.r); got != c.want {
			t.Errorf("%s: Bypass = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCacheControl(t *testing.T) {
	cases := map[int]string{
		0:     "no-cache",
		-1:    "no-cache",
		60:    "public, max-age=60, s-maxage=60, stale-while-revalidate=60",
		86400: "public, max-age=86400, s-maxage=86400, stale-while-revalidate=86400",
	}
	for ttl, want := range cases {
		if got := CacheControl(ttl); got != want {
			t.Errorf("CacheControl(%d) = %q, want %q", ttl, got, want)
		}
	}
}

func TestETagMatch(t *testing.T) {
	tag := ETag([]byte("hello"))
	if tag == ETag([]byte("hello!")) || !strings.HasPrefix(tag, `"`) || len(tag) != 34 {
		t.Fatalf("bad etag %s", tag)
	}
	cases := map[string]bool{
		"":                       false,
		tag:                      true,
		"W/" + tag:               true,
		`"other", ` + tag:        true,
		`"other"`:                false,
		"*":                      true,
		`"other",W/` + tag + ` `: true,
	}
	for h, want := range cases {
		if got := Match(h, tag); got != want {
			t.Errorf("Match(%q) = %v, want %v", h, got, want)
		}
	}
}

func entry(group string, body int) *Entry {
	return &Entry{Group: group, Body: make([]byte, body), Expires: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestEvictionBySize(t *testing.T) {
	per := entry("/a/", 1000).size("k1")
	c := New(3*per, 2*per)
	for _, k := range []string{"k1", "k2", "k3"} {
		if !c.Put(k, entry("/a/", 1000), c.Gen()) {
			t.Fatalf("put %s refused", k)
		}
	}
	c.Get("k1") // k2 is now least recently used
	c.Put("k4", entry("/a/", 1000), c.Gen())
	if _, ok := c.Get("k2"); ok {
		t.Error("k2 should have been evicted")
	}
	for _, k := range []string{"k1", "k3", "k4"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%s should be cached", k)
		}
	}
	if n, size := c.Stats(); n != 3 || size != 3*per {
		t.Errorf("stats = %d, %d; want 3, %d", n, size, 3*per)
	}

	if c.Put("big", entry("/a/", int(2*per)), c.Gen()) {
		t.Error("entry over the per-entry cap was stored")
	}
	if n, _ := c.Stats(); n != 3 {
		t.Errorf("a refused put must not evict, have %d entries", n)
	}

	c.Put("k1", entry("/a/", 10), c.Gen())
	if _, size := c.Stats(); size != 2*per+entry("/a/", 10).size("k1") {
		t.Errorf("replacing an entry must release its old size, size = %d", size)
	}
}

func TestExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := New(1<<20, 1<<20)
	c.now = func() time.Time { return now }
	e := entry("/", 10)
	e.Expires = now.Add(time.Minute)
	c.Put("k", e, c.Gen())
	if _, ok := c.Get("k"); !ok {
		t.Fatal("fresh entry missing")
	}
	now = now.Add(time.Minute)
	if _, ok := c.Get("k"); ok {
		t.Error("expired entry returned")
	}
	if n, size := c.Stats(); n != 0 || size != 0 {
		t.Errorf("expired entry not removed: %d, %d", n, size)
	}
}

func TestPurge(t *testing.T) {
	c := New(1<<20, 1<<20)
	put := func(key, group string) { c.Put(key, entry(group, 10), c.Gen()) }
	put("h/category/news/", "/category/news/")
	put("h/category/news/page/2/", "/category/news/")
	put("h/category/news/feed/", "/category/news/")
	put("h/category/news/?page=2", "/category/news/")
	put("h/category/other/", "/category/other/")
	put("h/", "/")

	c.Purge("/category/news/", "/missing/")
	if n, _ := c.Stats(); n != 2 {
		t.Errorf("after purge: %d entries, want 2", n)
	}
	if _, ok := c.Get("h/category/other/"); !ok {
		t.Error("other group was purged")
	}

	gen := c.Gen()
	c.Purge("/")
	if c.Put("h/", entry("/", 10), gen) {
		t.Error("a render that raced a purge was stored")
	}
	if !c.Put("h/", entry("/", 10), c.Gen()) {
		t.Error("put with the current generation refused")
	}

	c.PurgeAll()
	if n, size := c.Stats(); n != 0 || size != 0 {
		t.Errorf("after purge all: %d, %d", n, size)
	}
	if !c.Put("h/", entry("/", 10), c.Gen()) {
		t.Error("cache unusable after purge all")
	}
}
