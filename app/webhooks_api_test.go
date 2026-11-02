package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestWebhooksAPI(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	desc := "api " + env.suffix
	t.Cleanup(func() {
		_, _ = env.pool.Exec(ctx, `DELETE FROM jobs WHERE kind = 'webhooks.deliver' AND (payload->>'delivery_id')::bigint IN
			(SELECT d.id FROM plg_webhooks_deliveries d JOIN plg_webhooks_endpoints e ON e.id = d.endpoint_id WHERE e.description = $1)`, desc)
		_, _ = env.pool.Exec(ctx, `DELETE FROM plg_webhooks_endpoints WHERE description = $1`, desc)
		_, _ = env.pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_type = 'webhook' AND actor_user_id = $1`, env.ids["admin"])
	})
	const base = "/plugins/webhooks"
	body := func(url string, events ...string) string {
		return fmt.Sprintf(`{"url":%q,"description":%q,"events":["%s"],"enabled":true}`, url, desc, strings.Join(events, `","`))
	}

	if r := env.do("editor", http.MethodGet, base+"/endpoints", ""); r.code != http.StatusForbidden {
		t.Errorf("editor list: %d", r.code)
	}
	for b, field := range map[string]string{
		body("http://hooks.example.com/x", "post.saved"):    "url",
		body("https://127.0.0.1/x", "post.saved"):           "url",
		body("https://hooks.example.com/x", "post.nope"):    "events",
		`{"url":"https://hooks.example.com/x","events":[]}`: "events",
	} {
		r := env.do("admin", http.MethodPost, base+"/endpoints", b)
		if r.code != http.StatusBadRequest || !strings.Contains(string(r.body), `"field":"`+field+`"`) {
			t.Errorf("%s: %d %s", b, r.code, r.body)
		}
	}

	var created struct {
		Endpoint struct {
			ID         int64    `json:"id"`
			Events     []string `json:"events"`
			SecretHint string   `json:"secret_hint"`
		} `json:"endpoint"`
		Secret string `json:"secret"`
	}
	r := env.do("admin", http.MethodPost, base+"/endpoints", body("https://hooks.example.com/afsc", "post.saved", "comment.created", "post.saved"))
	r.json(t, &created)
	if r.code != http.StatusCreated || !strings.HasPrefix(created.Secret, "whsec_") || len(created.Endpoint.Events) != 2 ||
		!strings.HasSuffix(created.Secret, created.Endpoint.SecretHint) {
		t.Fatalf("create: %d %s", r.code, r.body)
	}
	id := fmt.Sprint(created.Endpoint.ID)

	r = env.do("admin", http.MethodGet, base+"/endpoints", "")
	if r.code != http.StatusOK || strings.Contains(string(r.body), created.Secret) || !strings.Contains(string(r.body), `"secret_unreadable":false`) {
		t.Errorf("list: %d %s", r.code, r.body)
	}
	if r := env.do("admin", http.MethodPut, base+"/endpoints/"+id, body("https://hooks.example.com/v2", "term.saved")); r.code != http.StatusOK ||
		!strings.Contains(string(r.body), "/v2") {
		t.Errorf("update: %d %s", r.code, r.body)
	}
	var rotated struct{ Secret string }
	r = env.do("admin", http.MethodPost, base+"/endpoints/"+id+"/rotate-secret", "")
	r.json(t, &rotated)
	if r.code != http.StatusOK || rotated.Secret == created.Secret || !strings.HasPrefix(rotated.Secret, "whsec_") {
		t.Errorf("rotate: %d %s", r.code, r.body)
	}

	var ping struct {
		DeliveryID int64 `json:"delivery_id"`
	}
	r = env.do("admin", http.MethodPost, base+"/endpoints/"+id+"/ping", "")
	r.json(t, &ping)
	if r.code != http.StatusAccepted || ping.DeliveryID == 0 {
		t.Fatalf("ping: %d %s", r.code, r.body)
	}
	r = env.do("admin", http.MethodGet, fmt.Sprintf("%s/deliveries/%d", base, ping.DeliveryID), "")
	if r.code != http.StatusOK || !strings.Contains(string(r.body), `"event":"ping"`) || !strings.Contains(string(r.body), "[redacted]") ||
		strings.Contains(string(r.body), rotated.Secret) {
		t.Errorf("delivery detail: %d %s", r.code, r.body)
	}
	var again struct {
		DeliveryID int64 `json:"delivery_id"`
	}
	r = env.do("admin", http.MethodPost, fmt.Sprintf("%s/deliveries/%d/redeliver", base, ping.DeliveryID), "")
	r.json(t, &again)
	var of int64
	_ = env.pool.QueryRow(ctx, `SELECT redelivery_of FROM plg_webhooks_deliveries WHERE id = $1`, again.DeliveryID).Scan(&of)
	if r.code != http.StatusAccepted || of != ping.DeliveryID {
		t.Errorf("redeliver: %d %s (redelivery_of %d)", r.code, r.body, of)
	}
	r = env.do("admin", http.MethodGet, base+"/deliveries?endpoint_id="+id+"&event=ping", "")
	if r.code != http.StatusOK || !strings.Contains(string(r.body), `"total":2`) {
		t.Errorf("deliveries: %d %s", r.code, r.body)
	}
	if r := env.do("admin", http.MethodGet, base+"/events", ""); r.code != http.StatusOK || !strings.Contains(string(r.body), "comment.status_changed") {
		t.Errorf("events: %d %s", r.code, r.body)
	}

	if r := env.do("admin", http.MethodDelete, base+"/endpoints/"+id, ""); r.code != http.StatusNoContent {
		t.Errorf("delete: %d %s", r.code, r.body)
	}
	if r := env.do("admin", http.MethodDelete, base+"/endpoints/"+id, ""); r.code != http.StatusNotFound {
		t.Errorf("delete again: %d", r.code)
	}
	var actions []string
	rows, err := env.pool.Query(ctx, `SELECT action || ' ' || coalesce(diff::text, '') FROM audit_log WHERE entity_type = 'webhook'
		AND entity_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	rows.Close()
	joined := strings.Join(actions, "\n")
	if len(actions) != 4 || strings.Contains(joined, "whsec_") {
		t.Errorf("audit entries:\n%s", joined)
	}
}
