// Package indexnow pings IndexNow (§8.6) with changed post and page URLs, from a job.
package indexnow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"afsc/internal/settings"
)

// Kind is the job kind; the payload is Payload.
const Kind = "indexnow.ping"

const Endpoint = "https://api.indexnow.org/indexnow"

type Payload struct {
	URLs []string `json:"urls"`
}

// Pinger is the job handler. Settings is read when the job runs, so a ping queued before IndexNow was switched
// off is dropped.
type Pinger struct {
	BaseURL  string
	Settings func() settings.IndexNow
	Client   *http.Client // nil means SafeClient()
	Endpoint string       // tests only; default Endpoint
}

type request struct {
	Host        string   `json:"host"`
	Key         string   `json:"key"`
	KeyLocation string   `json:"keyLocation"`
	URLList     []string `json:"urlList"`
}

func (p *Pinger) Handle(ctx context.Context, raw json.RawMessage) error {
	var pl Payload
	if err := json.Unmarshal(raw, &pl); err != nil {
		return fmt.Errorf("indexnow payload: %w", err)
	}
	cfg := p.Settings()
	if !cfg.Enabled || cfg.Key == "" || len(pl.URLs) == 0 {
		return nil
	}
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return err
	}
	body, err := json.Marshal(request{Host: base.Host, Key: cfg.Key, KeyLocation: p.BaseURL + "/" + cfg.Key + ".txt",
		URLList: pl.URLs})
	if err != nil {
		return err
	}
	endpoint, client := p.Endpoint, p.Client
	if endpoint == "" {
		endpoint = Endpoint
	}
	if client == nil {
		client = SafeClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("indexnow: %s", resp.Status)
	}
	return nil
}

var errPrivate = errors.New("indexnow: refusing to connect to a non-public address")

// SafeClient talks only to public addresses (checked at dial time, so redirects and DNS answers are covered), gives
// up after 10 s and follows at most 3 redirects.
func SafeClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !public(ap.Addr()) {
			return errPrivate
		}
		return nil
	}}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 5 * time.Second, MaxIdleConns: 2,
		IdleConnTimeout: 30 * time.Second, ForceAttemptHTTP2: true}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 || r.URL.Scheme != "https" {
			return errors.New("indexnow: redirect refused")
		}
		return nil
	}}
}

func public(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !a.IsLoopback() && !a.IsLinkLocalUnicast() &&
		!cgnat.Contains(a)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")
