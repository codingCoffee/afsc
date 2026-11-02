package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var basePathRe = regexp.MustCompile(`^(/[a-z0-9._-]+)+$`)

// publicURLs reads AFSC_PUBLIC_BASE_URL: an http(s) origin, optionally with a path the public site lives under
// ("https://example.com/varsity"). The admin and its API always stay at the origin's root.
func publicURLs() (origin, basePath string, err error) {
	raw := os.Getenv("AFSC_PUBLIC_BASE_URL")
	u, perr := url.Parse(raw)
	if raw == "" || perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("AFSC_PUBLIC_BASE_URL must be an http(s) URL such as https://blog.example.com " +
			"or https://example.com/blog")
	}
	p := strings.TrimRight(u.Path, "/")
	if p != "" && (!basePathRe.MatchString(p) || p == "/admin" || strings.HasPrefix(p, "/admin/")) {
		return "", "", errors.New("AFSC_PUBLIC_BASE_URL path must be lowercase segments of a-z 0-9 . _ - and not /admin")
	}
	return u.Scheme + "://" + u.Host, p, nil
}

// publicBaseURL is the origin of AFSC_PUBLIC_BASE_URL: the accepted Origin and the base of admin links.
func publicBaseURL() (string, error) {
	origin, _, err := publicURLs()
	return origin, err
}

// withBasePath serves the public site under prefix. Requests under it have the prefix stripped, root-relative
// Location headers and root-relative URLs in HTML responses get it added. Requests outside it (the admin,
// probes) pass through unchanged.
func withBasePath(h http.Handler, prefix string) http.Handler {
	if prefix == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == prefix {
			target := prefix + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		if !strings.HasPrefix(p, prefix+"/") {
			h.ServeHTTP(w, r)
			return
		}
		// The admin SPA is built for /admin/ and lives at the origin's root, never under the base path.
		if rest := strings.TrimPrefix(p, prefix); rest == "/admin" || strings.HasPrefix(rest, "/admin/") {
			target := rest
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(p, prefix)
		if r.URL.RawPath != "" {
			r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)
		}
		r2.RequestURI = r2.URL.RequestURI()
		pw := &prefixWriter{ResponseWriter: w, prefix: prefix}
		h.ServeHTTP(pw, r2)
		pw.finish()
	})
}

// prefixURL adds prefix to a root-relative URL ("/x", not "//host/x") that doesn't already carry it.
func prefixURL(v, prefix string) string {
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || v == prefix || strings.HasPrefix(v, prefix+"/") {
		return v
	}
	return prefix + v
}

type prefixWriter struct {
	http.ResponseWriter
	prefix   string
	wrote    bool
	status   int
	buffered bool
	buf      bytes.Buffer
}

func (pw *prefixWriter) WriteHeader(status int) {
	if pw.wrote {
		return
	}
	pw.wrote, pw.status = true, status
	hd := pw.Header()
	if loc := hd.Get("Location"); loc != "" {
		hd.Set("Location", prefixURL(loc, pw.prefix))
	}
	if strings.HasPrefix(hd.Get("Content-Type"), "text/html") && status != http.StatusNotModified {
		pw.buffered = true
		return
	}
	pw.ResponseWriter.WriteHeader(status)
}

func (pw *prefixWriter) Write(b []byte) (int, error) {
	if !pw.wrote {
		pw.WriteHeader(http.StatusOK)
	}
	if pw.buffered {
		return pw.buf.Write(b)
	}
	return pw.ResponseWriter.Write(b)
}

// finish writes a buffered HTML response with its URLs prefixed.
func (pw *prefixWriter) finish() {
	if !pw.buffered {
		return
	}
	out := prefixHTML(pw.buf.Bytes(), pw.prefix)
	pw.Header().Set("Content-Length", strconv.Itoa(len(out)))
	pw.ResponseWriter.WriteHeader(pw.status)
	_, _ = pw.ResponseWriter.Write(out)
}

var urlAttrs = map[string]bool{"href": true, "src": true, "poster": true, "action": true, "formaction": true}

// prefixHTML rewrites root-relative URL attributes. Tags it doesn't change are copied byte for byte.
func prefixHTML(in []byte, prefix string) []byte {
	z := html.NewTokenizer(bytes.NewReader(in))
	var out bytes.Buffer
	out.Grow(len(in) + len(in)/50)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.Bytes()
		}
		raw := z.Raw()
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		raw = append([]byte(nil), raw...)
		t := z.Token()
		changed := false
		for i, a := range t.Attr {
			var nv string
			switch {
			case a.Namespace == "" && urlAttrs[a.Key]:
				nv = prefixURL(a.Val, prefix)
			case a.Namespace == "" && a.Key == "srcset":
				nv = prefixSrcset(a.Val, prefix)
			default:
				continue
			}
			if nv != a.Val {
				t.Attr[i].Val, changed = nv, true
			}
		}
		if changed {
			out.WriteString(t.String())
		} else {
			out.Write(raw)
		}
	}
}

func prefixSrcset(v, prefix string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		f := strings.Fields(p)
		if len(f) == 0 {
			continue
		}
		f[0] = prefixURL(f[0], prefix)
		parts[i] = strings.Join(f, " ")
	}
	return strings.Join(parts, ", ")
}
