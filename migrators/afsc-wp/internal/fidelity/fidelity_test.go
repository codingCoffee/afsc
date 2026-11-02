package fidelity

import (
	"regexp"
	"strings"
	"testing"
)

var wpImg = regexp.MustCompile(`^wp-image-(\d+)$`)

func side(prefix string) Side {
	return Side{
		Hosts: map[string]bool{"blog.example.com": true},
		Media: func(u string, cls []string) (string, bool) {
			for _, c := range cls {
				if m := wpImg.FindStringSubmatch(c); m != nil {
					return "wp:post:" + m[1], true
				}
			}
			if strings.Contains(u, prefix+"a") {
				return "wp:post:5", true
			}
			return "", false
		},
	}
}

func TestCompare(t *testing.T) {
	wp := `<p>Hello “world” <a href="https://blog.example.com/about/">About</a></p>
<p><a href="https://blog.example.com/wp-content/uploads/a.jpg"><img class="alignleft wp-image-5" src="https://blog.example.com/wp-content/uploads/a-300x200.jpg" width="300"></a> After</p>
<h2>Title</h2><p>https://www.youtube.com/watch?v=abcdefghijk</p>`
	afMinor := `<p>Hello “world” <a href="/about">About</a></p>
<p><a href="/m/1/abcd/orig/a.jpg"><img class="wp-image-5 alignleft" src="/m/1/abcd/w300/a.jpg" srcset="x" loading="lazy"></a></p><p>After</p>
<h2 id="t">Title</h2><iframe src="https://www.youtube-nocookie.com/embed/abcdefghijk"></iframe>`
	class, diffs := Compare(Analyze(wp, side("uploads/")), Analyze(afMinor, side("/m/1/abcd/orig/")), false)
	if class != Minor {
		t.Fatalf("want minor, got %s %v", class, diffs)
	}
	if class, _ := Compare(Analyze(wp, side("uploads/")), Analyze(wp, side("uploads/")), false); class != Exact {
		t.Fatalf("identical bodies must be exact, got %s", class)
	}
	if class, _ := Compare(Analyze(wp, side("uploads/")), Analyze(afMinor, side("/m/1/abcd/orig/")), true); class != Fallback {
		t.Fatalf("want fallback, got %s", class)
	}
	lossy := strings.Replace(afMinor, "About", "Abut", 1)
	class, diffs = Compare(Analyze(wp, side("uploads/")), Analyze(lossy, side("/m/1/abcd/orig/")), false)
	if class != Lossy || !strings.HasPrefix(diffs[0], "text:") {
		t.Fatalf("want lossy text diff, got %s %v", class, diffs)
	}
	noImage := strings.Replace(afMinor, `<img class="wp-image-5 alignleft" src="/m/1/abcd/w300/a.jpg" srcset="x" loading="lazy">`, "", 1)
	if class, diffs := Compare(Analyze(wp, side("uploads/")), Analyze(noImage, side("/m/1/abcd/orig/")), false); class != Lossy || !strings.Contains(strings.Join(diffs, ";"), "images") {
		t.Fatalf("want image diff, got %s %v", class, diffs)
	}
}

func TestEmbedsAndScripts(t *testing.T) {
	a := Analyze(`<blockquote class="twitter-tweet"><p>Tweet text</p><a href="https://twitter.com/x/status/1">link</a></blockquote><script>var a=1;</script>`, Side{})
	if a.Text != "" || len(a.Embeds) != 1 || a.Embeds[0] != "x:https://x.com/x/status/1" {
		t.Fatalf("got %+v", a)
	}
}
