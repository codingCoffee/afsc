package indexnow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"

	"afsc/internal/settings"
)

func TestHandle(t *testing.T) {
	var got request
	calls := 0
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("%s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()

	cfg := settings.IndexNow{Enabled: true, Key: "abcdef123456"}
	p := &Pinger{BaseURL: "https://blog.example.com", Settings: func() settings.IndexNow { return cfg },
		Client: srv.Client(), Endpoint: srv.URL}
	payload, _ := json.Marshal(Payload{URLs: []string{"https://blog.example.com/a/", "https://blog.example.com/b/"}})
	ctx := context.Background()

	if err := p.Handle(ctx, payload); err != nil {
		t.Fatal(err)
	}
	want := request{Host: "blog.example.com", Key: "abcdef123456", KeyLocation: "https://blog.example.com/abcdef123456.txt",
		URLList: []string{"https://blog.example.com/a/", "https://blog.example.com/b/"}}
	if got.Host != want.Host || got.Key != want.Key || got.KeyLocation != want.KeyLocation || !slices.Equal(got.URLList, want.URLList) {
		t.Errorf("request = %+v", got)
	}

	status = http.StatusTooManyRequests
	if err := p.Handle(ctx, payload); err == nil {
		t.Error("a 429 must fail the job so it retries")
	}

	cfg.Enabled = false
	calls = 0
	if err := p.Handle(ctx, payload); err != nil || calls != 0 {
		t.Errorf("disabled: err %v, calls %d", err, calls)
	}
}

func TestSafeClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := SafeClient().Get(srv.URL); err == nil {
		t.Error("loopback request succeeded")
	}
	for addr, want := range map[string]bool{"8.8.8.8": true, "10.0.0.1": false, "127.0.0.1": false, "169.254.169.254": false,
		"100.64.1.1": false, "::1": false, "fd00::1": false, "2606:4700::1111": true, "::ffff:10.0.0.1": false} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("public(%s) = %v", addr, got)
		}
	}
}
