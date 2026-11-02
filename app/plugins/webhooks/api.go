package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/plugins"
	"afsc/internal/safehttp"
)

const (
	maxEndpoints   = 50
	maxEvents      = 20
	deliveriesPage = 50
)

// EventInfo describes a subscribable event in the admin.
type EventInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

var eventInfo = map[string]string{
	"post.saved":             "A post or page was created, saved or restored from trash.",
	"post.published":         "A post or page went live, by a save or on its schedule.",
	"post.unpublished":       "A published post or page was moved back to another status or to trash.",
	"post.deleted":           "A post or page was deleted from trash for good.",
	"term.saved":             "A category or tag was created or edited.",
	"comment.created":        "A comment was posted (not spam) or a staff member replied.",
	"comment.status_changed": "A comment was approved, unapproved, marked spam, trashed, restored or deleted.",
	"media.uploaded":         "A file was added to the media library.",
	"user.login":             "Someone signed in.",
	"settings.changed":       "A settings group or the spam blocklist was saved with changes.",
}

func (p *Plugin) routes(h *plugins.Host) {
	m := authz.ManageSettings
	h.RegisterAdminAPI(http.MethodGet, "events", m, http.HandlerFunc(p.events))
	h.RegisterAdminAPI(http.MethodGet, "endpoints", m, http.HandlerFunc(p.listEndpoints))
	h.RegisterAdminAPI(http.MethodPost, "endpoints", m, http.HandlerFunc(p.createEndpoint))
	h.RegisterAdminAPI(http.MethodPut, "endpoints/:id", m, http.HandlerFunc(p.updateEndpoint))
	h.RegisterAdminAPI(http.MethodDelete, "endpoints/:id", m, http.HandlerFunc(p.deleteEndpoint))
	h.RegisterAdminAPI(http.MethodPost, "endpoints/:id/rotate-secret", m, http.HandlerFunc(p.rotateSecret))
	h.RegisterAdminAPI(http.MethodPost, "endpoints/:id/ping", m, http.HandlerFunc(p.ping))
	h.RegisterAdminAPI(http.MethodGet, "deliveries", m, http.HandlerFunc(p.listDeliveries))
	h.RegisterAdminAPI(http.MethodGet, "deliveries/:id", m, http.HandlerFunc(p.getDelivery))
	h.RegisterAdminAPI(http.MethodPost, "deliveries/:id/redeliver", m, http.HandlerFunc(p.redeliver))
}

func (p *Plugin) events(w http.ResponseWriter, _ *http.Request) {
	out := make([]EventInfo, 0, len(plugins.EventNames))
	for _, n := range plugins.EventNames {
		out = append(out, EventInfo{Name: n, Description: eventInfo[n]})
	}
	plugins.JSON(w, http.StatusOK, map[string]any{"events": out})
}

// Endpoint is a configured receiver. The secret itself is only ever returned by create and rotate.
type Endpoint struct {
	ID               int64     `json:"id"`
	URL              string    `json:"url"`
	Description      string    `json:"description"`
	Events           []string  `json:"events"`
	Enabled          bool      `json:"enabled"`
	SecretHint       string    `json:"secret_hint"`
	SecretUnreadable bool      `json:"secret_unreadable"`
	LastStatus       *string   `json:"last_status"`
	Failures24h      int64     `json:"failures_24h"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const sqlEndpoints = `
SELECT e.id, e.url, e.description, e.events, e.enabled, e.secret_hint, e.secret_enc, e.created_at, e.updated_at,
  (SELECT status FROM plg_webhooks_deliveries d WHERE d.endpoint_id = e.id ORDER BY d.created_at DESC, d.id DESC LIMIT 1),
  (SELECT count(*) FROM plg_webhooks_deliveries d WHERE d.endpoint_id = e.id AND d.status = 'failed'
     AND d.created_at > now() - interval '24 hours')
FROM plg_webhooks_endpoints e`

func (p *Plugin) loadEndpoints(ctx context.Context, where string, args ...any) ([]Endpoint, error) {
	rows, err := p.h.DB().Query(ctx, sqlEndpoints+" "+where+" ORDER BY e.id", args...)
	if err != nil {
		return nil, err
	}
	key, keyErr := p.h.Key()
	out := []Endpoint{}
	var e Endpoint
	var enc []byte
	_, err = pgx.ForEachRow(rows, []any{&e.ID, &e.URL, &e.Description, &e.Events, &e.Enabled, &e.SecretHint, &enc,
		&e.CreatedAt, &e.UpdatedAt, &e.LastStatus, &e.Failures24h}, func() error {
		e.SecretUnreadable = keyErr != nil
		if keyErr == nil {
			_, err := unseal(key, e.ID, enc)
			e.SecretUnreadable = err != nil
		}
		out = append(out, e)
		return nil
	})
	return out, err
}

func (p *Plugin) listEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := p.loadEndpoints(r.Context(), "")
	if err != nil {
		p.fail(w, err)
		return
	}
	plugins.JSON(w, http.StatusOK, map[string]any{"items": eps})
}

type endpointInput struct {
	URL         string   `json:"url"`
	Description string   `json:"description"`
	Events      []string `json:"events"`
	Enabled     bool     `json:"enabled"`
}

func (in *endpointInput) validate() (string, string) {
	in.URL, in.Description = strings.TrimSpace(in.URL), strings.TrimSpace(in.Description)
	if err := safehttp.ValidateURL(in.URL); err != nil {
		return "url", "The URL " + err.Error() + "."
	}
	if utf8.RuneCountInString(in.Description) > 200 {
		return "description", "At most 200 characters."
	}
	if len(in.Events) == 0 || len(in.Events) > maxEvents {
		return "events", fmt.Sprintf("Choose between 1 and %d events.", maxEvents)
	}
	slices.Sort(in.Events)
	in.Events = slices.Compact(in.Events)
	for _, ev := range in.Events {
		if !slices.Contains(plugins.EventNames, ev) {
			return "events", fmt.Sprintf("Unknown event %q.", ev)
		}
	}
	return "", ""
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		plugins.Error(w, http.StatusBadRequest, "", "invalid request body")
		return false
	}
	return true
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(plugins.PathParam(r, "id"), 10, 64)
	return id
}

func (p *Plugin) fail(w http.ResponseWriter, err error) {
	p.h.Logf("%v", err)
	plugins.Error(w, http.StatusInternalServerError, "", "internal error")
}

func (p *Plugin) audit(ctx context.Context, tx pgx.Tx, r *http.Request, action string, id int64, diff any) error {
	a, _ := plugins.AdminFrom(r.Context())
	return audit.Write(ctx, tx, audit.Entry{Actor: uuid.NullUUID{UUID: a.User, Valid: a.User != uuid.Nil}, Action: action,
		EntityType: "webhook", EntityID: strconv.FormatInt(id, 10), Diff: diff, IP: a.IP})
}

// withTx runs fn in a transaction and commits when it returns no error and hasn't written a response.
func (p *Plugin) withTx(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, tx pgx.Tx) (bool, error)) bool {
	ctx := r.Context()
	tx, err := p.h.DB().Begin(ctx)
	if err != nil {
		p.fail(w, err)
		return false
	}
	defer tx.Rollback(ctx)
	done, err := fn(ctx, tx)
	if err == nil && !done {
		err = tx.Commit(ctx)
	}
	if err != nil {
		p.fail(w, err)
		return false
	}
	return !done
}

type secretResponse struct {
	Endpoint Endpoint `json:"endpoint"`
	Secret   string   `json:"secret"`
}

func (p *Plugin) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var in endpointInput
	if !decode(w, r, &in) {
		return
	}
	if field, msg := in.validate(); field != "" {
		plugins.Error(w, http.StatusBadRequest, field, msg)
		return
	}
	key, err := p.h.Key()
	if err != nil {
		p.fail(w, err)
		return
	}
	secret := newSecret()
	var id int64
	ok := p.withTx(w, r, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM plg_webhooks_endpoints`).Scan(&n); err != nil {
			return false, err
		}
		if n >= maxEndpoints {
			plugins.Error(w, http.StatusConflict, "", fmt.Sprintf("At most %d endpoints.", maxEndpoints))
			return true, nil
		}
		if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('plg_webhooks_endpoints', 'id'))`).Scan(&id); err != nil {
			return false, err
		}
		enc, err := seal(key, id, secret)
		if err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO plg_webhooks_endpoints (id, url, description, events, enabled, secret_enc, secret_hint)
VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, in.URL, in.Description, in.Events, in.Enabled, enc, hint(secret)); err != nil {
			return false, err
		}
		return false, p.audit(ctx, tx, r, "webhook.create", id, in)
	})
	if ok {
		p.respondSecret(w, r, http.StatusCreated, id, secret)
	}
}

func (p *Plugin) respondSecret(w http.ResponseWriter, r *http.Request, status int, id int64, secret string) {
	eps, err := p.loadEndpoints(r.Context(), "WHERE e.id = $1", id)
	if err != nil || len(eps) == 0 {
		p.fail(w, errors.Join(err, errors.New("endpoint vanished")))
		return
	}
	plugins.JSON(w, status, secretResponse{Endpoint: eps[0], Secret: secret})
}

// lockEndpoint loads an endpoint for update; it writes the 404 itself.
func lockEndpoint(ctx context.Context, tx pgx.Tx, w http.ResponseWriter, id int64) (*endpointInput, bool, error) {
	var cur endpointInput
	err := tx.QueryRow(ctx, `SELECT url, description, events, enabled FROM plg_webhooks_endpoints WHERE id = $1 FOR UPDATE`, id).
		Scan(&cur.URL, &cur.Description, &cur.Events, &cur.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		plugins.Error(w, http.StatusNotFound, "", "endpoint not found")
		return nil, true, nil
	}
	return &cur, false, err
}

func (p *Plugin) updateEndpoint(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var in endpointInput
	if !decode(w, r, &in) {
		return
	}
	if field, msg := in.validate(); field != "" {
		plugins.Error(w, http.StatusBadRequest, field, msg)
		return
	}
	ok := p.withTx(w, r, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		cur, done, err := lockEndpoint(ctx, tx, w, id)
		if done || err != nil {
			return done, err
		}
		if _, err := tx.Exec(ctx, `
UPDATE plg_webhooks_endpoints SET url = $2, description = $3, events = $4, enabled = $5, updated_at = now() WHERE id = $1`,
			id, in.URL, in.Description, in.Events, in.Enabled); err != nil {
			return false, err
		}
		diff := map[string]audit.Change{}
		if cur.URL != in.URL {
			diff["url"] = audit.Change{From: cur.URL, To: in.URL}
		}
		if cur.Description != in.Description {
			diff["description"] = audit.Change{From: cur.Description, To: in.Description}
		}
		if !slices.Equal(cur.Events, in.Events) {
			diff["events"] = audit.Change{From: cur.Events, To: in.Events}
		}
		if cur.Enabled != in.Enabled {
			diff["enabled"] = audit.Change{From: cur.Enabled, To: in.Enabled}
		}
		if len(diff) == 0 {
			return false, nil
		}
		return false, p.audit(ctx, tx, r, "webhook.update", id, diff)
	})
	if !ok {
		return
	}
	eps, err := p.loadEndpoints(r.Context(), "WHERE e.id = $1", id)
	if err != nil || len(eps) == 0 {
		p.fail(w, errors.Join(err, errors.New("endpoint vanished")))
		return
	}
	plugins.JSON(w, http.StatusOK, eps[0])
}

func (p *Plugin) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	ok := p.withTx(w, r, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		cur, done, err := lockEndpoint(ctx, tx, w, id)
		if done || err != nil {
			return done, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM plg_webhooks_endpoints WHERE id = $1`, id); err != nil {
			return false, err
		}
		return false, p.audit(ctx, tx, r, "webhook.delete", id, map[string]any{"url": cur.URL, "events": cur.Events})
	})
	if ok {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (p *Plugin) rotateSecret(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	key, err := p.h.Key()
	if err != nil {
		p.fail(w, err)
		return
	}
	secret := newSecret()
	ok := p.withTx(w, r, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		if _, done, err := lockEndpoint(ctx, tx, w, id); done || err != nil {
			return done, err
		}
		enc, err := seal(key, id, secret)
		if err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE plg_webhooks_endpoints SET secret_enc = $2, secret_hint = $3, updated_at = now() WHERE id = $1`,
			id, enc, hint(secret)); err != nil {
			return false, err
		}
		return false, p.audit(ctx, tx, r, "webhook.rotate", id, nil)
	})
	if ok {
		p.respondSecret(w, r, http.StatusOK, id, secret)
	}
}

func (p *Plugin) ping(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var deliveryID int64
	ok := p.withTx(w, r, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		if _, done, err := lockEndpoint(ctx, tx, w, id); done || err != nil {
			return done, err
		}
		data, _ := json.Marshal(map[string]int64{"endpoint_id": id})
		b, err := json.Marshal(body{Event: "ping", OccurredAt: p.Now().UTC(), Data: data})
		if err != nil {
			return false, err
		}
		deliveryID, err = queue(ctx, tx, id, uuid.NewString(), "ping", b, nil)
		return false, err
	})
	if ok {
		plugins.JSON(w, http.StatusAccepted, map[string]int64{"delivery_id": deliveryID})
	}
}

// Delivery is one attempt series to one endpoint.
type Delivery struct {
	ID             int64      `json:"id"`
	EndpointID     int64      `json:"endpoint_id"`
	EndpointURL    string     `json:"endpoint_url"`
	EventID        string     `json:"event_id"`
	Event          string     `json:"event"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	LastStatusCode *int       `json:"last_status_code"`
	LastError      *string    `json:"last_error"`
	DurationMS     *int       `json:"duration_ms"`
	RedeliveryOf   *int64     `json:"redelivery_of"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}

// DeliveryDetail adds what was sent and what came back. The signature is redacted; it changes per attempt.
type DeliveryDetail struct {
	Delivery
	Payload         json.RawMessage   `json:"payload"`
	RequestHeaders  map[string]string `json:"request_headers"`
	ResponseSnippet *string           `json:"response_snippet"`
}

const deliveryCols = `d.id, d.endpoint_id, e.url, d.event_id::text, d.event, d.status, d.attempts, d.last_status_code,
  d.last_error, d.duration_ms, d.redelivery_of, d.created_at, d.completed_at`

func (d *Delivery) dest() []any {
	return []any{&d.ID, &d.EndpointID, &d.EndpointURL, &d.EventID, &d.Event, &d.Status, &d.Attempts, &d.LastStatusCode,
		&d.LastError, &d.DurationMS, &d.RedeliveryOf, &d.CreatedAt, &d.CompletedAt}
}

var deliveryStatuses = []string{"pending", "succeeded", "failed", "cancelled"}

func (p *Plugin) listDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	endpointID, _ := strconv.ParseInt(q.Get("endpoint_id"), 10, 64)
	status, event := q.Get("status"), q.Get("event")
	if status != "" && !slices.Contains(deliveryStatuses, status) {
		plugins.Error(w, http.StatusBadRequest, "status", "unknown status")
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	rows, err := p.h.DB().Query(r.Context(), `
SELECT `+deliveryCols+`, count(*) OVER ()
FROM plg_webhooks_deliveries d JOIN plg_webhooks_endpoints e ON e.id = d.endpoint_id
WHERE ($1::bigint = 0 OR d.endpoint_id = $1) AND ($2::text = '' OR d.status = $2) AND ($3::text = '' OR d.event = $3)
ORDER BY d.created_at DESC, d.id DESC LIMIT $4 OFFSET $5`, endpointID, status, event, deliveriesPage, (page-1)*deliveriesPage)
	if err != nil {
		p.fail(w, err)
		return
	}
	items := []Delivery{}
	var total int64
	var d Delivery
	if _, err := pgx.ForEachRow(rows, append(d.dest(), &total), func() error {
		items = append(items, d)
		return nil
	}); err != nil {
		p.fail(w, err)
		return
	}
	plugins.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": deliveriesPage})
}

func (p *Plugin) getDelivery(w http.ResponseWriter, r *http.Request) {
	var d DeliveryDetail
	var payload string
	err := p.h.DB().QueryRow(r.Context(), `
SELECT `+deliveryCols+`, d.payload::text, d.response_snippet
FROM plg_webhooks_deliveries d JOIN plg_webhooks_endpoints e ON e.id = d.endpoint_id WHERE d.id = $1`, pathID(r)).
		Scan(append(d.dest(), &payload, &d.ResponseSnippet)...)
	if errors.Is(err, pgx.ErrNoRows) {
		plugins.Error(w, http.StatusNotFound, "", "delivery not found")
		return
	}
	if err != nil {
		p.fail(w, err)
		return
	}
	d.Payload = json.RawMessage(payload)
	d.RequestHeaders = headers(d.Event, d.EventID, d.ID)
	d.RequestHeaders["X-AFSC-Signature"] = "t=…,v1=[redacted]"
	plugins.JSON(w, http.StatusOK, d)
}

func (p *Plugin) redeliver(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var newID int64
	ok := p.withTx(w, r, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		var endpointID int64
		var eventID, event, payload string
		err := tx.QueryRow(ctx, `SELECT endpoint_id, event_id::text, event, payload::text FROM plg_webhooks_deliveries WHERE id = $1`, id).
			Scan(&endpointID, &eventID, &event, &payload)
		if errors.Is(err, pgx.ErrNoRows) {
			plugins.Error(w, http.StatusNotFound, "", "delivery not found")
			return true, nil
		}
		if err != nil {
			return false, err
		}
		newID, err = queue(ctx, tx, endpointID, eventID, event, []byte(payload), &id)
		return false, err
	})
	if ok {
		plugins.JSON(w, http.StatusAccepted, map[string]int64{"delivery_id": newID})
	}
}
