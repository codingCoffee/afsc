// Package indexnow pings IndexNow (§8.6) with changed post and page URLs, from a job.
package indexnow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"afsc/internal/safehttp"
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
	Client   *http.Client // nil means a safehttp client following up to 3 redirects
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
		client = safehttp.Client(safehttp.Options{MaxRedirects: 3})
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
