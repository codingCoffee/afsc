package site

import (
	"net/http"
	"slices"
	"strings"

	"afsc/internal/settings"
	"afsc/internal/theme"
)

// embedFrameHosts are the iframe origins content.Render emits for embed nodes.
var embedFrameHosts = []string{"https://www.youtube-nocookie.com", "https://player.vimeo.com", "https://open.spotify.com",
	"https://w.soundcloud.com", "https://www.google.com"}

// cspCore is the default policy; directives in settings.CSPDirectives also take theme, admin and hash sources.
var cspCore = [][]string{
	{"default-src", "'self'"},
	{"script-src", "'self'"},
	{"style-src", "'self'", "'unsafe-inline'"},
	{"img-src", "'self'", "data:"},
	{"font-src", "'self'", "data:"},
	{"connect-src", "'self'"},
	{"media-src", "'self'"},
	slices.Concat([]string{"frame-src", "'self'"}, embedFrameHosts),
	{"object-src", "'none'"},
	{"base-uri", "'self'"},
	{"form-action", "'self'"},
	{"frame-ancestors", "'self'"},
}

// buildCSP composes the public policy from the core defaults, theme.json csp, the admin's extra hosts and the
// hashes of allowed inline scripts. Sources are hashed rather than nonced so cached pages and 304s stay valid.
func buildCSP(themeCSP map[string][]string, extra map[string][]string, scriptHashes []string) string {
	parts := make([]string, 0, len(cspCore))
	for _, d := range cspCore {
		name, srcs := d[0], slices.Clone(d[1:])
		srcs = append(srcs, themeCSP[name]...)
		srcs = append(srcs, extra[name]...)
		if name == "script-src" {
			srcs = append(srcs, scriptHashes...)
		}
		var uniq []string
		for _, s := range srcs {
			if !slices.Contains(uniq, s) {
				uniq = append(uniq, s)
			}
		}
		parts = append(parts, name+" "+strings.Join(uniq, " "))
	}
	return strings.Join(parts, "; ")
}

// securityHeaderSet is every header the public site adds to its responses for one snapshot and theme version.
func securityHeaderSet(s *settings.All, baseURL string, m *theme.Manifest, themeScripts []string) http.Header {
	var themeCSP map[string][]string
	if m != nil {
		themeCSP = m.CSP.Directives()
	}
	hashes := slices.Concat(themeScripts, theme.InlineScriptHashes([]byte(s.Snippets.HeadHTML)),
		theme.InlineScriptHashes([]byte(s.Snippets.BodyEndHTML)))
	slices.Sort(hashes)
	hashes = slices.Compact(hashes)
	name := "Content-Security-Policy"
	if s.Security.CSPReportOnly {
		name = "Content-Security-Policy-Report-Only"
	}
	h := http.Header{}
	h.Set(name, buildCSP(themeCSP, s.Security.ExtraCSPHosts, hashes))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if s.Security.HSTS && strings.HasPrefix(baseURL, "https://") {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
	return h
}

type secHeaders struct {
	snap     *snapshot
	manifest *theme.Manifest
	header   http.Header
}

// setSecurityHeaders adds the public security headers, rebuilding them only when the snapshot or theme changed.
func (st *Site) setSecurityHeaders(w http.ResponseWriter, snap *snapshot) {
	var m *theme.Manifest
	var scripts []string
	if st.theme != nil {
		m, scripts = st.theme.InlineScripts()
	}
	cur := st.sec.Load()
	if cur == nil || cur.snap != snap || cur.manifest != m {
		cur = &secHeaders{snap: snap, manifest: m, header: securityHeaderSet(&snap.settings, st.cfg.BaseURL, m, scripts)}
		st.sec.Store(cur)
	}
	h := w.Header()
	for k, v := range cur.header {
		h[k] = v
	}
}

// Theme is the loaded theme, for the admin's theme settings screen.
func (st *Site) Theme() *theme.Theme { return st.theme }
