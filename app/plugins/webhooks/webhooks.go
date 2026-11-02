// Package webhooks sends signed HTTP POSTs to admin-configured endpoints when events happen (build plan §12). Each
// event becomes one delivery row per subscribed endpoint, sent and retried by the webhooks.deliver job.
package webhooks

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/jobs"
	"afsc/internal/plugins"
	"afsc/internal/safehttp"
)

//go:embed migrations
var migrations embed.FS

const (
	DeliverKind  = "webhooks.deliver"
	PruneKind    = "webhooks.prune"
	MaxAttempts  = 8
	retention    = 30 * 24 * time.Hour
	snippetBytes = 1024
	userAgent    = "AFSC-Webhooks/1"
)

type Plugin struct {
	h *plugins.Host
	// Client sends deliveries; nil uses a safehttp client that follows no redirects. Tests inject one.
	Client *http.Client
	Now    func() time.Time
}

func New() *Plugin { return &Plugin{} }

func (*Plugin) ID() string { return "webhooks" }

func (*Plugin) Info() plugins.Info {
	return plugins.Info{Name: "Webhooks", DefaultEnabled: true,
		Description: "Signed HTTP POSTs to your endpoints when posts, terms, comments, media, logins or settings change. Set them up under Webhooks."}
}

func (p *Plugin) Init(_ context.Context, h *plugins.Host) error {
	p.h = h
	if p.Client == nil {
		p.Client = safehttp.Client(safehttp.Options{})
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	h.RegisterMigrations(sub)
	plugins.AddAllActions(h, 0, p.fanOut)
	h.RegisterJob(DeliverKind, p.deliver, jobs.Options{Backoff: Backoff})
	h.RegisterJob(PruneKind, p.prune, jobs.Options{})
	h.RegisterSchedule(jobs.Schedule{Kind: PruneKind, Every: 24 * time.Hour})
	p.routes(h)
	return nil
}

// Backoff is 1 min × 4^(attempt−1), capped at 12 h, ±25 %: about 30 h over 8 attempts.
func Backoff(attempt int) time.Duration {
	d := time.Minute
	for i := 1; i < attempt && d < 12*time.Hour; i++ {
		d *= 4
	}
	d = min(d, 12*time.Hour)
	return d*3/4 + rand.N(d/2)
}

// body is exactly what receivers get: §12's {event, occurred_at, data}.
type body struct {
	Event      string          `json:"event"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

const sqlFanOut = `
WITH d AS (
  INSERT INTO plg_webhooks_deliveries (endpoint_id, event_id, event, payload)
  SELECT id, $1, $2, $3::json FROM plg_webhooks_endpoints WHERE enabled AND $2 = ANY(events)
  ON CONFLICT (endpoint_id, event_id) WHERE redelivery_of IS NULL DO NOTHING
  RETURNING id
)
INSERT INTO jobs (kind, payload, max_attempts)
SELECT $4, jsonb_build_object('delivery_id', id), $5 FROM d`

// fanOut queues one delivery per enabled endpoint subscribed to the event. Re-running it adds nothing.
func (p *Plugin) fanOut(ctx context.Context, ev plugins.Envelope[json.RawMessage]) error {
	b, err := json.Marshal(body{Event: ev.Event, OccurredAt: ev.OccurredAt, Data: ev.Data})
	if err != nil {
		return err
	}
	_, err = p.h.DB().Exec(ctx, sqlFanOut, ev.ID, ev.Event, string(b), DeliverKind, MaxAttempts)
	return err
}

// queue adds a delivery outside the event flow (ping, redeliver) and its job, in tx.
func queue(ctx context.Context, tx pgx.Tx, endpointID int64, eventID, event string, payload []byte, redeliveryOf *int64) (int64, error) {
	var id int64
	if err := tx.QueryRow(ctx, `
INSERT INTO plg_webhooks_deliveries (endpoint_id, event_id, event, payload, redelivery_of)
VALUES ($1, $2, $3, $4::json, $5) RETURNING id`, endpointID, eventID, event, string(payload), redeliveryOf).Scan(&id); err != nil {
		return 0, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO jobs (kind, payload, max_attempts) VALUES ($1, jsonb_build_object('delivery_id', $2::bigint), $3)`,
		DeliverKind, id, MaxAttempts)
	return id, err
}

type outcome struct {
	status   string // succeeded, failed, or pending (retry)
	code     *int
	err      string
	snippet  string
	duration time.Duration
}

// retryable is a response worth trying again: rate limits, timeouts and server errors.
func retryable(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooEarly || code == http.StatusTooManyRequests || code >= 500
}

func (p *Plugin) deliver(ctx context.Context, raw json.RawMessage) error {
	var j struct {
		DeliveryID int64 `json:"delivery_id"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return err
	}
	db := p.h.DB()
	var (
		eventID, event, status, url string
		payload                     []byte
		attempts                    int
		endpointID                  int64
		enabled                     bool
		secretEnc                   []byte
	)
	err := db.QueryRow(ctx, `
SELECT d.event_id::text, d.event, d.payload::text, d.status, d.attempts, e.id, e.url, e.enabled, e.secret_enc
FROM plg_webhooks_deliveries d JOIN plg_webhooks_endpoints e ON e.id = d.endpoint_id WHERE d.id = $1`, j.DeliveryID).
		Scan(&eventID, &event, &payload, &status, &attempts, &endpointID, &url, &enabled, &secretEnc)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "pending") {
		return nil
	}
	if err != nil {
		return err
	}
	if !enabled {
		return p.record(ctx, j.DeliveryID, attempts, outcome{status: "cancelled", err: "endpoint disabled"})
	}
	attempts++
	o := p.send(ctx, j.DeliveryID, endpointID, eventID, event, url, payload, secretEnc)
	if o.status == "pending" && attempts >= MaxAttempts {
		o.status = "failed"
	}
	if err := p.record(ctx, j.DeliveryID, attempts, o); err != nil {
		return err
	}
	if o.status == "pending" {
		return errors.New(o.err)
	}
	return nil
}

func (p *Plugin) send(ctx context.Context, deliveryID, endpointID int64, eventID, event, url string, payload, secretEnc []byte) outcome {
	key, err := p.h.Key()
	if err != nil {
		return outcome{status: "failed", err: err.Error()}
	}
	secret, err := unseal(key, endpointID, secretEnc)
	if err != nil {
		return outcome{status: "failed", err: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return outcome{status: "failed", err: err.Error()}
	}
	for k, v := range headers(event, eventID, deliveryID) {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-AFSC-Signature", Sign(secret, p.Now().Unix(), payload))
	start := time.Now()
	resp, err := p.Client.Do(req)
	o := outcome{duration: time.Since(start)}
	if err != nil {
		o.status, o.err = "pending", err.Error()
		return o
	}
	defer resp.Body.Close()
	snip, _ := io.ReadAll(io.LimitReader(resp.Body, snippetBytes))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	code := resp.StatusCode
	o.code, o.snippet = &code, cleanText(snip)
	switch {
	case code >= 200 && code < 300:
		o.status = "succeeded"
	case retryable(code):
		o.status, o.err = "pending", resp.Status
	case code >= 300 && code < 400:
		o.status, o.err = "failed", resp.Status+": redirects aren't followed; update the endpoint URL"
	default:
		o.status, o.err = "failed", resp.Status
	}
	return o
}

// headers are sent with every delivery; X-AFSC-Signature is added per attempt.
func headers(event, eventID string, deliveryID int64) map[string]string {
	return map[string]string{"Content-Type": "application/json", "User-Agent": userAgent, "X-AFSC-Event": event,
		"X-AFSC-Event-Id": eventID, "X-AFSC-Delivery-Id": strconv.FormatInt(deliveryID, 10)}
}

func (p *Plugin) record(ctx context.Context, id int64, attempts int, o outcome) error {
	var errText *string
	if o.err != "" {
		e := cleanText([]byte(o.err))
		errText = &e
	}
	var snippet *string
	if o.snippet != "" {
		snippet = &o.snippet
	}
	var ms *int
	if o.duration > 0 {
		v := int(o.duration.Milliseconds())
		ms = &v
	}
	_, err := p.h.DB().Exec(context.WithoutCancel(ctx), `
UPDATE plg_webhooks_deliveries SET status = $2, attempts = $3, last_status_code = $4, last_error = $5,
  response_snippet = $6, duration_ms = $7, completed_at = CASE WHEN $2 = 'pending' THEN NULL ELSE now() END
WHERE id = $1`, id, o.status, attempts, o.code, errText, snippet, ms)
	if err != nil {
		return fmt.Errorf("webhooks: record delivery %d: %w", id, err)
	}
	return nil
}

// cleanText makes response bytes storable as text: valid UTF-8 without NULs.
func cleanText(b []byte) string {
	return strings.ReplaceAll(strings.ToValidUTF8(string(b), "�"), "\x00", "")
}

func (p *Plugin) prune(ctx context.Context, _ json.RawMessage) error {
	_, err := p.h.DB().Exec(ctx, `DELETE FROM plg_webhooks_deliveries WHERE created_at < now() - make_interval(secs => $1)`,
		retention.Seconds())
	return err
}
