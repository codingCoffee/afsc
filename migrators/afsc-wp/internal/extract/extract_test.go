package extract

import (
	"testing"
	"time"
)

func TestFixMojibake(t *testing.T) {
	cases := []struct {
		in, want string
		fixed    bool
	}{
		{"Itâ€™s a test", "It’s a test", true},
		{"cafÃ©", "café", true},
		{"â‚¹500 fee", "₹500 fee", true},
		{"café", "café", false},
		{"plain ascii", "plain ascii", false},
		{"It’s fine", "It’s fine", false},
		// a genuine character outside cp1252 means the field isn't double-encoded as a whole
		{"Itâ€™s ✓", "Itâ€™s ✓", false},
	}
	for _, c := range cases {
		got, fixed := FixMojibake(c.in)
		if got != c.want || fixed != c.fixed {
			t.Errorf("%q: got %q %v, want %q %v", c.in, got, fixed, c.want, c.fixed)
		}
	}
}

func TestDecodeSlug(t *testing.T) {
	cases := map[string]string{
		"hello-world":                 "hello-world",
		"caf%c3%a9-culture":           "café-culture",
		"%e0%a4%a8%e0%a4%ae%e0%a4%b8": "नमस",
		"bad%zzslug":                  "bad%zzslug",
	}
	for in, want := range cases {
		if got := DecodeSlug(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestUTCDates(t *testing.T) {
	kolkata, _ := time.LoadLocation("Asia/Kolkata")
	got, err := UTC("0000-00-00 00:00:00", "2021-03-04 10:30:00", kolkata)
	if err != nil || !got.Equal(time.Date(2021, 3, 4, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("zero gmt fallback: %v %v", got, err)
	}
	got, _ = UTC("2021-03-04 05:00:00", "ignored", kolkata)
	if !got.Equal(time.Date(2021, 3, 4, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("gmt: %v", got)
	}
	if got, _ := UTC("0000-00-00 00:00:00", "0000-00-00 00:00:00", kolkata); !got.IsZero() {
		t.Fatalf("both zero: %v", got)
	}
}

func TestSiteLocation(t *testing.T) {
	cases := []struct {
		tz, off, name string
		fixed         bool
		offset        int
	}{
		{"Asia/Kolkata", "", "Asia/Kolkata", false, 19800},
		{"", "5.5", "", true, 19800},
		{"", "-5", "Etc/GMT+5", true, -18000},
		{"UTC+2", "", "Etc/GMT-2", true, 7200},
		{"", "0", "UTC", true, 0},
	}
	for _, c := range cases {
		loc, name, fixed, err := SiteLocation(c.tz, c.off)
		if err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		_, off := time.Date(2021, 1, 1, 0, 0, 0, 0, loc).Zone()
		if name != c.name || fixed != c.fixed || off != c.offset {
			t.Errorf("%v: got %s %v %d", c, name, fixed, off)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	d := &Dataset{
		Posts:   []*Post{{ID: 2, Type: "post", Meta: Meta{"_thumbnail_id": {"5"}}}, {ID: 1, Type: "page"}},
		Options: map[string]any{"posts_per_page": int64(10), "sticky_posts": []any{int64(3)}},
		Prefix:  "wp_",
	}
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Posts[0].ID != 1 || got.Post(2).Meta.First("_thumbnail_id") != "5" {
		t.Fatalf("posts: %+v", got.Posts)
	}
	if got.OptionString("posts_per_page") != "10" || got.Prefix != "wp_" {
		t.Fatalf("options: %+v", got.Options)
	}
	if s, _ := got.Options["sticky_posts"].([]any); len(s) != 1 || s[0] != int64(3) {
		t.Fatalf("sticky: %#v", got.Options["sticky_posts"])
	}
}
