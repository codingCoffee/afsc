// Package safehttp is an HTTP client for URLs that admins or plugins supply (IndexNow, webhooks). It connects only
// to public addresses, checked at dial time so DNS answers and redirects can't reach internal services.
package safehttp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var ErrBlocked = errors.New("refusing to connect to a non-public address")

// blocked are the IANA special-purpose ranges that aren't caught by netip's private, loopback and link-local checks,
// plus translation prefixes that can embed an internal IPv4 address.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

// Public reports whether a is a globally routable unicast address. IPv4-mapped addresses are judged as IPv4.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

type Options struct {
	// MaxRedirects is how many https redirects to follow. 0 returns the 3xx response to the caller.
	MaxRedirects int
}

// Client gives up after 10 s, speaks TLS 1.2+, ignores proxy environment variables and dials public addresses only.
func Client(o Options) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !Public(ap.Addr()) {
			return fmt.Errorf("%w: %s", ErrBlocked, address)
		}
		return nil
	}}
	tr := &http.Transport{Proxy: nil, DialContext: d.DialContext, TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second,
		ForceAttemptHTTP2: true}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if o.MaxRedirects == 0 {
			return http.ErrUseLastResponse
		}
		if len(via) > o.MaxRedirects || r.URL.Scheme != "https" {
			return errors.New("redirect refused")
		}
		return nil
	}}
}

// ValidateURL gives quick feedback when a URL is saved: https, a host, no credentials or fragment, and not an
// address that's obviously internal. The dial check in Client is what actually enforces it.
func ValidateURL(raw string) error {
	if len(raw) > 2000 {
		return errors.New("must be at most 2000 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return errors.New("must be an https:// URL")
	}
	if u.User != nil {
		return errors.New("must not contain a username or password")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return errors.New("must not contain a #fragment")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("must be a public address")
	}
	if a, err := netip.ParseAddr(host); err == nil && !Public(a) {
		return errors.New("must be a public address")
	}
	return nil
}
