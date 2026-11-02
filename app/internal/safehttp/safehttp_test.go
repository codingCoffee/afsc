package safehttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.215.14": true, "1.1.1.1": true, "2606:4700:4700::1111": true, "::ffff:93.184.215.14": true,
		"0.0.0.0": false, "0.1.2.3": false, "10.0.0.1": false, "100.64.0.1": false, "127.0.0.1": false,
		"169.254.169.254": false, "172.16.0.1": false, "192.0.0.8": false, "192.0.2.1": false, "192.168.1.1": false,
		"198.18.0.1": false, "198.19.255.255": false, "198.51.100.1": false, "203.0.113.1": false, "224.0.0.1": false,
		"240.0.0.1": false, "255.255.255.255": false, "::": false, "::1": false, "fc00::1": false, "fd12::1": false,
		"fe80::1": false, "ff02::1": false, "64:ff9b::a00:1": false, "64:ff9b:1::1": false, "100::1": false,
		"2001::1": false, "2001:db8::1": false, "2002:a00:1::1": false, "::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v", addr, got)
		}
	}
}

func TestClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	t.Setenv("HTTPS_PROXY", srv.URL)
	t.Setenv("HTTP_PROXY", srv.URL)
	_, err := Client(Options{}).Get(srv.URL)
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("loopback: %v", err)
	}
	if tr := Client(Options{}).Transport.(*http.Transport); tr.Proxy != nil || tr.TLSClientConfig.MinVersion < 0x0303 {
		t.Error("proxy env must be ignored and TLS 1.2 required")
	}
}

func TestRedirects(t *testing.T) {
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	none := Client(Options{}).CheckRedirect
	if err := none(req("https://a.example/"), []*http.Request{req("https://b.example/")}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("MaxRedirects 0 must return the 3xx: %v", err)
	}
	three := Client(Options{MaxRedirects: 3}).CheckRedirect
	via := []*http.Request{req("https://b.example/")}
	if err := three(req("https://a.example/"), via); err != nil {
		t.Errorf("one https hop: %v", err)
	}
	if err := three(req("http://a.example/"), via); err == nil {
		t.Error("a redirect to http was followed")
	}
	if err := three(req("https://a.example/"), append(via, via[0], via[0], via[0])); err == nil {
		t.Error("a fourth redirect was followed")
	}
}

func TestValidateURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://hooks.example.com/afsc":       true,
		"https://hooks.example.com:8443/x?y=1": true,
		"https://93.184.215.14/hook":           true,
		"http://hooks.example.com/":            false,
		"https://user:pw@hooks.example.com/":   false,
		"https://hooks.example.com/#frag":      false,
		"https:///nohost":                      false,
		"https://localhost/":                   false,
		"https://api.localhost./":              false,
		"https://127.0.0.1/":                   false,
		"https://[::1]/":                       false,
		"https://169.254.169.254/latest":       false,
		"https://[::ffff:10.0.0.1]/":           false,
		"ftp://hooks.example.com/":             false,
		"not a url":                            false,
	} {
		if err := ValidateURL(raw); (err == nil) != ok {
			t.Errorf("ValidateURL(%q) = %v", raw, err)
		}
	}
}
