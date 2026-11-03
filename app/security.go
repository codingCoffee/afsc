package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/labstack/echo/v5"
	"golang.org/x/net/html"
)

const ctxClientIP = "client_ip"

// trustedProxies parses AFSC_TRUSTED_PROXIES: comma-separated CIDRs or addresses. Empty trusts nobody.
func trustedProxies() ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range strings.Split(os.Getenv("AFSC_TRUSTED_PROXIES"), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			a, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("AFSC_TRUSTED_PROXIES: %q is not an address or CIDR", s)
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("AFSC_TRUSTED_PROXIES: %q is not an address or CIDR", s)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// realIP walks X-Forwarded-For from the right, skipping trusted proxies. The header is ignored unless the direct
// peer is itself trusted, so a client can't spoof its address.
func realIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !isTrusted(peer, trusted) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		if !isTrusted(a, trusted) {
			return a.String()
		}
		peer = a
	}
	return peer.String()
}

func clientIPMiddleware(trusted []netip.Prefix) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set(ctxClientIP, realIP(c.Request(), trusted))
			return next(c)
		}
	}
}

// clientIP is the address set by clientIPMiddleware, or the direct peer when the middleware didn't run.
func clientIP(c *echo.Context) string {
	if ip, ok := c.Get(ctxClientIP).(string); ok {
		return ip
	}
	return realIP(c.Request(), nil)
}

// rawBodyRoutes may send a non-JSON body (CSV import, media upload). The Origin check still applies.
var rawBodyRoutes = map[string]bool{
	"POST " + apiPrefix + "/redirects/import": true,
	"POST " + apiPrefix + "/media":            true,
	"PUT " + apiPrefix + "/media/:id/file":    true,
}

// apiGuard is the CSRF layer on top of SameSite=Strict and Bearer tokens: every unsafe request must come from
// the admin's own origin, and must carry JSON unless its route takes a raw body. route() adds it to every route.
func apiGuard(origin string, rawBody bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			r := c.Request()
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				return next(c)
			}
			if r.Header.Get("Origin") != origin {
				return jsonError(c, http.StatusForbidden, "cross-origin request refused")
			}
			if rawBody || (r.ContentLength == 0 && len(r.TransferEncoding) == 0) {
				return next(c)
			}
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
				return jsonError(c, http.StatusUnsupportedMediaType, "send application/json")
			}
			return next(c)
		}
	}
}

// adminHeaders keeps admin responses out of caches and search engines, and frames off.
func adminHeaders(csp string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Response().Header()
			h.Set("Cache-Control", "no-store")
			h.Set("X-Robots-Tag", "noindex, nofollow")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("X-Frame-Options", "DENY")
			if csp != "" {
				h.Set("Content-Security-Policy", csp)
			}
			return next(c)
		}
	}
}

const apiCSP = "default-src 'none'; frame-ancestors 'none'"

// adminCSP is the SPA's policy. Inline scripts in the served index.html are allowed by hash, so no
// 'unsafe-inline' is needed for scripts. Styles allow inline because the UI library positions popovers with
// style attributes.
func adminCSP(index []byte) (string, error) {
	hashes, err := inlineScriptHashes(index)
	if err != nil {
		return "", err
	}
	script := "'self'"
	for _, h := range hashes {
		script += " '" + h + "'"
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		"img-src 'self' data: blob: https:",
		"media-src 'self' blob: https:",
		"connect-src 'self'",
		"frame-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; "), nil
}

// inlineScriptHashes returns 'sha256-…' sources for every inline <script> CSP checks: executable ones and import
// maps, which Nuxt uses to resolve #entry in lazily loaded chunks.
func inlineScriptHashes(doc []byte) ([]string, error) {
	z := html.NewTokenizer(bytes.NewReader(doc))
	var out []string
	inScript := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			if err := z.Err(); err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return out, nil
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" {
				continue
			}
			inScript = true
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				switch string(k) {
				case "src":
					inScript = false
				case "type":
					if t := strings.ToLower(string(v)); t != "" && t != "module" && t != "text/javascript" && t != "importmap" {
						inScript = false // data blocks such as application/json never execute
					}
				}
			}
		case html.TextToken:
			if inScript {
				sum := sha256.Sum256(z.Raw())
				out = append(out, "sha256-"+base64.StdEncoding.EncodeToString(sum[:]))
			}
		case html.EndTagToken:
			inScript = false
		}
	}
}
