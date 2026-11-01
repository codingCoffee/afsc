package site

import (
	"testing"
	"time"

	"afsc/internal/settings"
)

func TestNoiseMatcher(t *testing.T) {
	re := noiseMatcher(settings.DefaultNoisePatterns())
	for path, noise := range map[string]bool{
		"/wp-login.php":             true,
		"/xmlrpc.php":               true,
		"/blog/WP-LOGIN.PHP":        true,
		"/.env":                     true,
		"/.env.production":          true,
		"/.git/config":              true,
		"/wp-admin/":                true,
		"/wp-admin/install.php":     true,
		"/default.aspx":             true,
		"/cgi-bin/test.cgi":         true,
		"/wp-admin":                 false,
		"/php-tips/":                false,
		"/a/.env":                   false,
		"/old-post/":                false,
		"/wp-content/uploads/a.jpg": false,
	} {
		if got := re.MatchString(path); got != noise {
			t.Errorf("%s: noise = %v, want %v", path, got, noise)
		}
	}
	if re := noiseMatcher([]string{"/a?c/", "/x.y*"}); !re.MatchString("/abc/") || re.MatchString("/abbc/") ||
		!re.MatchString("/x.yz") || re.MatchString("/xzy") {
		t.Error("? is one character and . is literal")
	}
	if noiseMatcher(nil) != nil {
		t.Error("no patterns must match nothing")
	}
}

func TestRedirectHitsCounted(t *testing.T) {
	st := testSite(t, nil)
	st.rh = newRedirectHits()
	for _, p := range []string{"/old-path/", "/old-path", "/archive/2015/x/", "/topics/news/", "/example-post/"} {
		resolveStr(t, st, p)
	}
	if h := st.rh.hits; h[1].n != 2 || h[2].n != 1 || h[3].n != 1 || len(h) != 3 || !h[1].last.Equal(testNow) {
		t.Errorf("hits = %+v", h)
	}
	st.rh.merge(map[int64]redirectHit{1: {n: 5, last: testNow.Add(time.Hour)}})
	if h := st.rh.hits[1]; h.n != 7 || !h.last.Equal(testNow.Add(time.Hour)) {
		t.Errorf("merge = %+v", h)
	}
}
