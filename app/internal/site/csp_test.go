package site

import (
	"strings"
	"testing"

	"afsc/internal/settings"
	"afsc/internal/theme"
)

func TestCSPComposition(t *testing.T) {
	cases := []struct {
		name    string
		mod     func(*settings.All)
		csp     theme.CSP
		scripts []string
		header  string
		want    []string
	}{
		{name: "core", header: "Content-Security-Policy", want: []string{
			"default-src 'self'", "script-src 'self';", "style-src 'self' 'unsafe-inline';", "object-src 'none'",
			"frame-src 'self' https://www.youtube-nocookie.com https://player.vimeo.com", "frame-ancestors 'self'",
		}},
		{
			name: "theme, extra hosts and hashes",
			mod: func(a *settings.All) {
				a.Security.ExtraCSPHosts = map[string][]string{"script-src": {"https://cdn.example.com", "https://theme.example.com"},
					"frame-src": {"https://*.example.org"}}
				a.Snippets.HeadHTML = `<script>window.a=1</script><script src="https://cdn.example.com/x.js"></script>`
				a.Snippets.BodyEndHTML = `<script type="application/ld+json">{}</script>`
			},
			csp:     theme.CSP{Script: []string{"https://theme.example.com"}, Font: []string{"https://fonts.example.net"}},
			scripts: []string{"'sha256-theme'"},
			header:  "Content-Security-Policy",
			want: []string{
				"script-src 'self' https://theme.example.com https://cdn.example.com " +
					theme.InlineScriptHashes([]byte("<script>window.a=1</script>"))[0] + " 'sha256-theme';",
				"font-src 'self' data: https://fonts.example.net;",
				"https://www.google.com https://*.example.org;",
			},
		},
		{name: "report only", mod: func(a *settings.All) { a.Security.CSPReportOnly = true }, header: "Content-Security-Policy-Report-Only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			all := settings.Defaults()
			if c.mod != nil {
				c.mod(&all)
			}
			if err := all.Validate(); err != nil {
				t.Fatal(err)
			}
			h := securityHeaderSet(&all, "https://blog.example.com", &theme.Manifest{CSP: c.csp}, c.scripts, nil)
			got := h.Get(c.header)
			if got == "" {
				t.Fatalf("no %s header in %v", c.header, h)
			}
			other := "Content-Security-Policy-Report-Only"
			if c.header == other {
				other = "Content-Security-Policy"
			}
			if h.Get(other) != "" {
				t.Errorf("both CSP headers set")
			}
			for _, w := range c.want {
				if !strings.Contains(got+";", w) {
					t.Errorf("missing %q in %s", w, got)
				}
			}
			if strings.Count(got, "https://theme.example.com") > 1 {
				t.Errorf("duplicate source in %s", got)
			}
			if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "strict-origin-when-cross-origin" ||
				h.Get("Strict-Transport-Security") != "max-age=31536000" {
				t.Errorf("security headers: %v", h)
			}
		})
	}

	t.Run("hsts only on https and when enabled", func(t *testing.T) {
		all := settings.Defaults()
		if h := securityHeaderSet(&all, "http://localhost:8080", nil, nil, nil); h.Get("Strict-Transport-Security") != "" {
			t.Error("HSTS on http")
		}
		all.Security.HSTS = false
		if h := securityHeaderSet(&all, "https://blog.example.com", nil, nil, nil); h.Get("Strict-Transport-Security") != "" {
			t.Error("HSTS while disabled")
		}
	})

	t.Run("invalid hosts rejected", func(t *testing.T) {
		for _, h := range []string{"*", "'unsafe-inline'", "'unsafe-eval'", "http://a.example.com", "https://", "https://a.example.com/path",
			"https://*", "example.com", "https://a.example.com;script-src"} {
			all := settings.Defaults()
			all.Security.ExtraCSPHosts = map[string][]string{"script-src": {h}}
			if all.Validate() == nil {
				t.Errorf("%q accepted", h)
			}
		}
		all := settings.Defaults()
		all.Security.ExtraCSPHosts = map[string][]string{"media-src": {"https://a.example.com"}}
		if all.Validate() == nil {
			t.Error("unknown directive accepted")
		}
		for _, h := range []string{"https://a.example.com", "https://*.example.com", "https://a.example.com:8443"} {
			if !settings.ValidCSPHost(h) {
				t.Errorf("%q rejected", h)
			}
		}
	})
}
