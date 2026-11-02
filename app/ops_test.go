package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"afsc/internal/plugins"
	registry "afsc/plugins"
)

func probe(t *testing.T, h echo.HandlerFunc) (int, map[string]string, http.Header) {
	t.Helper()
	e := echo.New()
	e.GET("/p", h)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p", nil))
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body, rec.Header()
}

func testSets(t *testing.T) []migSet {
	t.Helper()
	rt, err := plugins.Build(context.Background(), plugins.Config{}, registry.All())
	if err != nil {
		t.Fatal(err)
	}
	sets, err := migrationSets(rt)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) < 3 || sets[0].Name != "core" || sets[0].Want == 0 {
		t.Fatalf("sets %+v", sets)
	}
	return sets
}

func TestHealthz(t *testing.T) {
	code, body, h := probe(t, healthz)
	if code != http.StatusOK || body["status"] != "ok" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("%d %v %v", code, body, h)
	}
}

func TestReadyz(t *testing.T) {
	pool := testPool(t)
	sets := testSets(t)
	if err := requireMigrations(context.Background(), pool, sets); err != nil {
		t.Fatalf("the test database must be migrated: %v", err)
	}
	if code, body, h := probe(t, readyz(pool, sets)); code != http.StatusOK || body["db"] != "ok" ||
		body["migrations"] != "ok" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("ready: %d %v", code, body)
	}

	behind := append([]migSet(nil), sets...)
	behind[len(behind)-1].Want = 9999
	if code, body, _ := probe(t, readyz(pool, behind)); code != http.StatusServiceUnavailable || body["migrations"] != "behind" {
		t.Errorf("behind: %d %v", code, body)
	}
	err := requireMigrations(context.Background(), pool, behind)
	if err == nil || !strings.Contains(err.Error(), behind[len(behind)-1].Name) || !strings.Contains(err.Error(), "afsc migrate up") {
		t.Errorf("startup with a migration behind: %v", err)
	}
	missing := []migSet{{Name: "plugin nope", Table: "plg_nope_schema_migrations", Want: 1}}
	if err := requireMigrations(context.Background(), pool, missing); err == nil || !strings.Contains(err.Error(), "nothing applied") {
		t.Errorf("never migrated: %v", err)
	}

	down, err := pgxpool.New(context.Background(), "postgres://afsc:x@127.0.0.1:1/afsc?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer down.Close()
	if code, body, _ := probe(t, readyz(down, sets)); code != http.StatusServiceUnavailable || body["db"] != "down" {
		t.Errorf("db down: %d %v", code, body)
	}
}

func TestMetricsListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := serveMetrics(ctx, "127.0.0.1:0", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + addr + "/debug/vars")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`"afsc_requests"`, `"afsc_page_cache"`, `"afsc_jobs"`, `"afsc_webhook_deliveries"`,
		`"afsc_plugin_failures"`, `"afsc_page_cache_size"`, `"memstats"`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("/debug/vars lacks %s", want)
		}
	}

	env := newAdminEnv(t)
	rec := httptest.NewRecorder()
	withRequestLog(env.api.e).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))
	if strings.Contains(rec.Body.String(), "memstats") {
		t.Error("the public server answers /debug/vars")
	}
}

func TestRequestLog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	h := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") == "" {
			t.Error("the handler didn't see the request id")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	}))
	serve := func(target, id string) (*httptest.ResponseRecorder, map[string]any) {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if id != "" {
			req.Header.Set("X-Request-ID", id)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var line map[string]any
		if buf.Len() > 0 {
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log line %q: %v", buf.String(), err)
			}
		}
		return rec, line
	}

	rec, line := serve("/search/?q=secret-term&preview=tok123", "")
	id := rec.Header().Get("X-Request-ID")
	if len(id) != 16 || line["request_id"] != id || line["path"] != "/search/" || line["status"] != float64(201) ||
		line["method"] != "GET" || line["bytes"] != float64(5) || line["latency_ms"] == nil || line["msg"] != "request" {
		t.Errorf("log line %v (id %q)", line, id)
	}
	if strings.Contains(buf.String(), "secret-term") || strings.Contains(buf.String(), "tok123") {
		t.Error("the query string was logged")
	}

	if rec, line := serve("/", "lb-1234abcd"); rec.Header().Get("X-Request-ID") != "lb-1234abcd" || line["request_id"] != "lb-1234abcd" {
		t.Error("a well-formed upstream request id was replaced")
	}
	if rec, _ := serve("/", "bad id\n{}"); rec.Header().Get("X-Request-ID") == "bad id\n{}" {
		t.Error("a malformed request id was kept")
	}
	if _, line := serve("/admin/api/v1/invites/abcdef0123456789", ""); line["path"] != "/admin/api/v1/invites/redacted" {
		t.Errorf("invite token logged: %v", line["path"])
	}
	if _, line := serve("/admin/invite/abcdef0123456789/", ""); line["path"] != "/admin/invite/redacted/" {
		t.Errorf("invite token logged: %v", line["path"])
	}
	if _, line := serve("/healthz", ""); line != nil {
		t.Errorf("a successful probe was logged: %v", line)
	}
}
