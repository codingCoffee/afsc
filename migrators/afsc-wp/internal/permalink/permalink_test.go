package permalink

import (
	"reflect"
	"testing"
	"time"

	"afsc/internal/settings"
)

func TestConvert(t *testing.T) {
	cases := []struct {
		in, pattern, trailing string
		unsupported           []string
	}{
		{"/%postname%/", "/{slug}/", "always", nil},
		{"/%year%/%monthnum%/%day%/%postname%/", "/{year}/{month}/{day}/{slug}/", "always", nil},
		{"/%category%/%postname%", "/{category}/{slug}/", "never", nil},
		{"/blog/%postname%/", "/blog/{slug}/", "always", nil},
		{"/%author%/%post_id%/", "/%author%/%post_id%/", "always", []string{"%author%", "%post_id%"}},
		{"/index.php/%postname%/", "/index.php/{slug}/", "always", []string{"index.php (PATHINFO permalinks)"}},
		{"", "", "always", []string{"(plain ?p= permalinks)"}},
	}
	for _, c := range cases {
		p, tr, un := Convert(c.in)
		if p != c.pattern || tr != c.trailing || !reflect.DeepEqual(un, c.unsupported) {
			t.Errorf("%q: got %q %q %v", c.in, p, tr, un)
		}
	}
}

func TestWPPaths(t *testing.T) {
	w := &WP{Structure: "/%year%/%monthnum%/%postname%/", CategoryBase: "/topics"}
	local := time.Date(2021, 3, 4, 23, 30, 0, 0, time.UTC)
	if got := w.Post(WPPost{ID: 5, Slug: "caf%c3%a9", Local: local}); got != "/2021/03/caf%c3%a9/" {
		t.Errorf("post %s", got)
	}
	if got := w.Page(9, []string{"about", "team"}); got != "/about/team/" {
		t.Errorf("page %s", got)
	}
	if got := w.Term("category", "news/local"); got != "/topics/news/local/" {
		t.Errorf("category %s", got)
	}
	if got := w.Term("post_tag", "x"); got != "/tag/x/" {
		t.Errorf("tag %s", got)
	}
	plain := &WP{}
	if got := plain.Post(WPPost{ID: 5}); got != "/?p=5" {
		t.Errorf("plain %s", got)
	}
}

func TestAFSCMatchesWP(t *testing.T) {
	kolkata, _ := time.LoadLocation("Asia/Kolkata")
	s := settings.Defaults().Permalinks
	s.PostPattern = "/{year}/{month}/{slug}/"
	a, err := NewAFSC(s, kolkata)
	if err != nil {
		t.Fatal(err)
	}
	// 2021-03-31 20:00 UTC is April 1st in Kolkata; both sides must use the local date.
	pub := time.Date(2021, 3, 31, 20, 0, 0, 0, time.UTC)
	w := &WP{Structure: "/%year%/%monthnum%/%postname%/"}
	if got, want := a.Post(1, "hello", pub, ""), w.Post(WPPost{Slug: "hello", Local: pub.In(kolkata)}); got != want {
		t.Errorf("afsc %s != wp %s", got, want)
	}
}
