package links

import "testing"

func TestRewrite(t *testing.T) {
	r := &Rewriter{
		Hosts: map[string]bool{"blog.example.com": true, "www.blog.example.com": true, "old.example.com": true},
		Resolve: func(param string, id int64) (string, bool) {
			switch {
			case param == "p" && id == 5:
				return "/hello-world/", true
			case param == "attachment_id" && id == 9:
				return "/wp-content/uploads/2021/03/a.jpg", true
			case param == "cat" && id == 3:
				return "/category/news/", true
			}
			return "", false
		},
		PathMap: map[string]string{"/old-name/": "/new-name/", "/café/": "/coffee/"},
	}
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"https://blog.example.com/about/", "/about/", true},
		{"http://www.blog.example.com/about/?x=1#top", "/about/?x=1#top", true},
		{"https://old.example.com/?p=5", "/hello-world/", true},
		{"/?p=5#comments", "/hello-world/#comments", true},
		{"https://blog.example.com/index.php?cat=3", "/category/news/", true},
		{"https://blog.example.com/?attachment_id=9", "/wp-content/uploads/2021/03/a.jpg", true},
		{"https://blog.example.com/?p=999", "/?p=999", true},
		{"https://blog.example.com/old-name", "/new-name/", true},
		{"https://blog.example.com/caf%C3%A9/", "/coffee/", true},
		{"https://blog.example.com/wp-content/uploads/2021/03/a-300x200.jpg", "/wp-content/uploads/2021/03/a-300x200.jpg", true},
		{"https://other.example.org/x", "https://other.example.org/x", true},
		{"relative/page.html", "relative/page.html", true},
		{"#section", "#section", true},
		{"mailto:user@example.com", "mailto:user@example.com", true},
		{"java\nscript:alert(1)", "", false},
		{"JavaScript:alert(1)", "", false},
	}
	for _, c := range cases {
		got, ok := r.Rewrite(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
