package pagecache

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxQueryRunes caps the q parameter, matching the search handler.
const MaxQueryRunes = 200

// reserved query params change what the resolver does (wpcompat, previews), so requests carrying them bypass.
// replytocom renders a reply form for one comment, which is per-visitor navigation that bots shouldn't fill the cache with.
var reserved = []string{"p", "page_id", "cat", "author", "attachment_id", "feed", "s", "preview", "replytocom"}

// CookiePrefix marks cookies that bypass the cache (previews, commenters). Other cookies, such as analytics, don't.
const CookiePrefix = "afsc_"

// Bypass reports whether r must skip the page cache entirely.
func Bypass(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return true
	}
	if r.Header.Get("Authorization") != "" {
		return true
	}
	for _, c := range r.Cookies() {
		if strings.HasPrefix(c.Name, CookiePrefix) {
			return true
		}
	}
	q, _ := url.ParseQuery(r.URL.RawQuery)
	for _, k := range reserved {
		if _, ok := q[k]; ok {
			return true
		}
	}
	return false
}

// Key builds the cache key from the site host, the request path and the allowed query params (q, page, cpage).
// It also returns the normalised query, which is all a cached page may depend on.
func Key(host string, u *url.URL) (key, query string) {
	in, _ := url.ParseQuery(u.RawQuery)
	out := url.Values{}
	if n, err := strconv.Atoi(in.Get("page")); err == nil && n > 1 {
		out.Set("page", strconv.Itoa(n))
	}
	if n, err := strconv.Atoi(in.Get("cpage")); err == nil && n > 1 {
		out.Set("cpage", strconv.Itoa(n))
	}
	if q := strings.TrimSpace(in.Get("q")); q != "" {
		if utf8.RuneCountInString(q) > MaxQueryRunes {
			q = string([]rune(q)[:MaxQueryRunes])
		}
		out.Set("q", q)
	}
	query = out.Encode()
	key = host + u.Path
	if query != "" {
		key += "?" + query
	}
	return key, query
}
