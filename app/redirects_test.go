package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"afsc/internal/db"
	"afsc/internal/importer"
	"afsc/internal/redirects"
	"afsc/internal/site"
	"afsc/internal/theme"
)

type discardStorage struct{}

func (discardStorage) Put(_ context.Context, _ string, body io.ReadSeeker, _ int64, _ string) error {
	_, err := io.Copy(io.Discard, body)
	return err
}

type apiClient struct {
	t      *testing.T
	e      *echo.Echo
	origin string // sent as Origin on every request when set
}

type apiResp struct {
	code int
	body []byte
	hdr  http.Header
}

func (r apiResp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (c apiClient) do(token, method, path, contentType, body string) apiResp {
	c.t.Helper()
	req := httptest.NewRequest(method, "/admin/api/v1"+path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	w := httptest.NewRecorder()
	c.e.ServeHTTP(w, req)
	return apiResp{code: w.Code, body: w.Body.Bytes(), hdr: w.Header()}
}

// redirectAPI imports the sample bundle, mounts the redirect routes and signs in one user per role.
func redirectAPI(t *testing.T) (apiClient, map[string]string, map[string]uuid.UUID, *pgxpool.Pool) {
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tm, err := theme.LoadManifest("themes/default")
	if err != nil {
		t.Fatal(err)
	}
	if rep, err := importer.Run(ctx, importer.Options{BundleDir: "testdata/bundle-sample", Theme: tm, Pool: pool,
		Storage: discardStorage{}}); err != nil {
		t.Fatalf("import: %v %+v", err, rep.Errors)
	}
	pub, err := site.New(ctx, site.Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "themes/default", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}

	h := newAuthHandler(pool, authConfig{secret: []byte(strings.Repeat("k", 32)), accessTTL: time.Minute, refreshTTL: time.Hour})
	e := echo.New()
	h.registerRedirects(e.Group("/admin/api/v1"), &redirectHandler{store: &redirects.Store{DB: pool, Router: pub.Router()}, site: pub})

	tokens, ids := map[string]string{}, map[string]uuid.UUID{}
	suffix := uuid.NewString()[:8]
	for _, role := range []string{"admin", "editor", "author", "contributor"} {
		u, err := h.q.CreateUserWithRole(ctx, db.CreateUserWithRoleParams{Username: "m2c-" + role + "-" + suffix,
			Email: "m2c-" + role + "-" + suffix + "@example.com", Role: role})
		if err != nil {
			t.Fatal(err)
		}
		sid := uuid.New()
		_, hash, _ := newRefreshToken()
		if _, err := h.q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{UserID: u.ID, SessionID: sid, TokenHash: hash,
			ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if tokens[role], err = h.signAccess(u.ID, role, sid); err != nil {
			t.Fatal(err)
		}
		ids[role] = u.ID
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			`DELETE FROM redirects WHERE origin <> 'import'`,
			`DELETE FROM not_found_log WHERE path ILIKE '/m2c-%'`,
			`DELETE FROM users WHERE username LIKE 'm2c-%-` + suffix + `'`,
		} {
			if _, err := pool.Exec(ctx, sql); err != nil {
				t.Error(err)
			}
		}
	})
	return apiClient{t: t, e: e}, tokens, ids, pool
}

type savedJSON struct {
	Redirect     redirects.Row `json:"redirect"`
	Warnings     []string      `json:"warnings"`
	FlattenedIDs []int64       `json:"flattened_ids"`
}

func TestRedirectAPIAuth(t *testing.T) {
	api, tokens, _, _ := redirectAPI(t)
	routes := []struct{ method, path, body string }{
		{"GET", "/redirects", ""},
		{"POST", "/redirects", `{"source":"/m2c-x/","target":"/"}`},
		{"GET", "/redirects/1", ""},
		{"PUT", "/redirects/1", `{"source":"/m2c-x/","target":"/"}`},
		{"DELETE", "/redirects/1", ""},
		{"POST", "/redirects/test", `{"source":"/a/","paths":["/a/"]}`},
		{"GET", "/redirects/export", ""},
		{"POST", "/redirects/import", "source,target\n/m2c-x/,/\n"},
		{"GET", "/not-found", ""},
		{"POST", "/not-found/dismiss", `{"paths":["/m2c-x/"]}`},
	}
	for _, r := range routes {
		if got := api.do("", r.method, r.path, "application/json", r.body).code; got != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: %d", r.method, r.path, got)
		}
		if got := api.do("not-a-jwt", r.method, r.path, "application/json", r.body).code; got != http.StatusUnauthorized {
			t.Errorf("%s %s with a bad token: %d", r.method, r.path, got)
		}
		for _, role := range []string{"author", "contributor"} {
			if got := api.do(tokens[role], r.method, r.path, "application/json", r.body).code; got != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d", r.method, r.path, role, got)
			}
		}
	}
	for _, role := range []string{"admin", "editor"} {
		if got := api.do(tokens[role], "GET", "/redirects", "", "").code; got != http.StatusOK {
			t.Errorf("GET /redirects as %s: %d", role, got)
		}
	}
}

func TestRedirectAPIFlow(t *testing.T) {
	api, tokens, ids, pool := redirectAPI(t)
	editor := tokens["editor"]
	ctx := context.Background()
	post := func(path, body string) apiResp { return api.do(editor, "POST", path, "application/json", body) }

	t.Run("409 then flatten", func(t *testing.T) {
		r := post("/redirects", `{"source":"/m2c-api/","target":"/old-path/"}`)
		var conflict struct {
			Code            string          `json:"code"`
			Field           string          `json:"field"`
			Hops            []redirects.Hop `json:"hops"`
			FlattenedTarget *string         `json:"flattened_target"`
		}
		r.json(t, &conflict)
		if r.code != http.StatusConflict || conflict.Code != "chain" || conflict.Field != "target" || len(conflict.Hops) != 1 ||
			conflict.FlattenedTarget == nil || *conflict.FlattenedTarget != "/example-post/" {
			t.Fatalf("%d %s", r.code, r.body)
		}
		r = post("/redirects?flatten=1", `{"source":"/m2c-api/","target":"/old-path/"}`)
		var saved savedJSON
		r.json(t, &saved)
		if r.code != http.StatusCreated || *saved.Redirect.Target != "/example-post/" || saved.Redirect.Origin != "manual" ||
			saved.Redirect.CreatedBy == nil || *saved.Redirect.CreatedBy != ids["editor"].String() {
			t.Fatalf("%d %s", r.code, r.body)
		}
		if r := post("/redirects", `{"source":"/m2c-loop/","target":"/m2c-loop-2/"}`); r.code != http.StatusCreated {
			t.Fatalf("%d %s", r.code, r.body)
		}
		r = post("/redirects?flatten=1", `{"source":"/m2c-loop-2/","target":"/m2c-loop/"}`)
		if r.code != http.StatusConflict || !strings.Contains(string(r.body), `"code":"loop"`) ||
			strings.Contains(string(r.body), `"flattened_target":"`) {
			t.Errorf("a loop can't be flattened: %d %s", r.code, r.body)
		}
	})

	t.Run("incoming redirects are flattened", func(t *testing.T) {
		r := post("/redirects", `{"source":"/m2c-c/","target":"/m2c-mid/","note":"to be flattened"}`)
		var c savedJSON
		r.json(t, &c)
		if r.code != http.StatusCreated || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "404") {
			t.Fatalf("%d %s", r.code, r.body)
		}
		r = post("/redirects", `{"source":"/M2C-Mid","target":"/about/","status":302}`)
		var mid savedJSON
		r.json(t, &mid)
		if r.code != http.StatusCreated || mid.Redirect.Source != "/m2c-mid/" || len(mid.FlattenedIDs) != 1 || mid.FlattenedIDs[0] != c.Redirect.ID {
			t.Fatalf("%d %s", r.code, r.body)
		}
		var row redirects.Row
		r = api.do(editor, "GET", "/redirects/"+itoa(c.Redirect.ID), "", "")
		r.json(t, &row)
		if *row.Target != "/about/" || row.Note != "to be flattened" {
			t.Errorf("flattened row %s", r.body)
		}

		r = api.do(editor, "PUT", "/redirects/"+itoa(c.Redirect.ID), "application/json", `{"source":"/m2c-c/","target":"/M2C-C"}`)
		if r.code != http.StatusBadRequest || !strings.Contains(string(r.body), `"field":"target"`) {
			t.Errorf("source == target: %d %s", r.code, r.body)
		}
		r = api.do(editor, "PUT", "/redirects/"+itoa(c.Redirect.ID), "application/json", `{"source":"/m2c-mid","target":"/"}`)
		if r.code != http.StatusConflict || !strings.Contains(string(r.body), `"field":"source"`) {
			t.Errorf("duplicate source: %d %s", r.code, r.body)
		}
		r = api.do(editor, "PUT", "/redirects/"+itoa(c.Redirect.ID), "application/json", `{"source":"/m2c-c/","match":"exact","target":null,"status":410}`)
		if r.code != http.StatusOK {
			t.Errorf("to 410: %d %s", r.code, r.body)
		}
		if r := api.do(editor, "PUT", "/redirects/999999999", "application/json", `{"source":"/m2c-z/","target":"/"}`); r.code != http.StatusNotFound {
			t.Errorf("unknown id: %d", r.code)
		}
		if r := post("/redirects", `{"source":"/m2c-z/","target":"/","extra":1}`); r.code != http.StatusBadRequest {
			t.Errorf("unknown field: %d", r.code)
		}
	})

	t.Run("list, delete and tester", func(t *testing.T) {
		var page struct {
			Items []redirects.Row `json:"items"`
			Total int64           `json:"total"`
		}
		r := api.do(editor, "GET", "/redirects?q=m2c-&sort=source&page_size=2", "", "")
		r.json(t, &page)
		if r.code != 200 || page.Total < 4 || len(page.Items) != 2 || page.Items[0].Source > page.Items[1].Source {
			t.Fatalf("%d %s", r.code, r.body)
		}
		r = api.do(editor, "GET", "/redirects?origin=import&match=regex", "", "")
		r.json(t, &page)
		if page.Total != 1 || page.Items[0].Match != "regex" {
			t.Errorf("filters: %s", r.body)
		}
		id := itoa(page.Items[0].ID)
		r = api.do(tokens["admin"], "GET", "/redirects?q=m2c-api", "", "")
		r.json(t, &page)
		if len(page.Items) != 1 {
			t.Fatalf("%s", r.body)
		}
		del := itoa(page.Items[0].ID)
		if r := api.do(editor, "DELETE", "/redirects/"+del, "", ""); r.code != http.StatusNoContent {
			t.Errorf("delete: %d %s", r.code, r.body)
		}
		if r := api.do(editor, "GET", "/redirects/"+del, "", ""); r.code != http.StatusNotFound {
			t.Errorf("after delete: %d", r.code)
		}
		if r := api.do(editor, "GET", "/redirects/"+id, "", ""); r.code != http.StatusOK {
			t.Errorf("imported regex: %d", r.code)
		}

		r = post("/redirects/test", `{"match":"regex","source":"^/topics/([a-z-]+)/?$","target":"/category/$1/","paths":["/topics/markets","/nope/"]}`)
		var tested struct {
			Results []redirects.TestResult `json:"results"`
		}
		r.json(t, &tested)
		if r.code != 200 || len(tested.Results) != 2 || !tested.Results[0].Matched || tested.Results[0].Target != "/category/markets/" ||
			tested.Results[1].Matched {
			t.Errorf("tester: %d %s", r.code, r.body)
		}
		if r := post("/redirects/test", `{"match":"regex","source":"(","paths":[]}`); r.code != http.StatusBadRequest {
			t.Errorf("bad regex: %d", r.code)
		}
	})

	t.Run("csv", func(t *testing.T) {
		csvPost := func(q, body string) apiResp { return api.do(editor, "POST", "/redirects/import"+q, "text/csv", body) }
		bad := "source,match,target,status,note\n/m2c-csv-1/,exact,/about/,301,ok\n/m2c-csv-2/,exact,/m2c-csv-2/,301,self\n"
		r := csvPost("", bad)
		var rep redirects.ImportReport
		r.json(t, &rep)
		if r.code != http.StatusUnprocessableEntity || len(rep.Errors) != 1 || rep.Errors[0].Line != 3 || rep.Created != 0 {
			t.Fatalf("%d %s", r.code, r.body)
		}
		if r := api.do(editor, "GET", "/redirects?q=m2c-csv", "", ""); !strings.Contains(string(r.body), `"total":0`) {
			t.Errorf("a rejected file must write nothing: %s", r.body)
		}

		good := "source,target,note\n/m2c-csv-1/,/m2c-csv-2/,first\n/m2c-csv-2/,/old-path/,second\n/m2c-csv-1/,/,dup\n"
		r = csvPost("", good)
		r.json(t, &rep)
		if r.code != http.StatusUnprocessableEntity || len(rep.Errors) != 2 {
			t.Errorf("chain and duplicate: %d %s", r.code, r.body)
		}
		good = "source,target,note\n/m2c-csv-1/,/m2c-csv-2/,first\n/m2c-csv-2/,/old-path/,second\n"
		r = csvPost("?flatten=1", good)
		r.json(t, &rep)
		if r.code != http.StatusOK || rep.Created != 2 || len(rep.Errors) != 0 {
			t.Fatalf("%d %s", r.code, r.body)
		}
		var target string
		if err := pool.QueryRow(ctx, `SELECT target FROM redirects WHERE source = '/m2c-csv-1/'`).Scan(&target); err != nil || target != "/example-post/" {
			t.Errorf("row 2 flattens row 1 too: %q %v", target, err)
		}
		// a row replaces the whole redirect, so a file without a note column clears notes
		r = csvPost("", "source,target,status\n/m2c-csv-1/,/about/,302\n")
		r.json(t, &rep)
		if r.code != http.StatusOK || rep.Updated != 1 || rep.Created != 0 {
			t.Errorf("update by source: %d %s", r.code, r.body)
		}
		if r := csvPost("", "nope\n/x/\n"); r.code != http.StatusBadRequest {
			t.Errorf("bad header: %d", r.code)
		}

		r = api.do(editor, "GET", "/redirects/export", "", "")
		body := string(r.body)
		if r.code != 200 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "text/csv") || !strings.HasPrefix(body, "source,match,target,status,note\n") ||
			!strings.Contains(body, "/m2c-csv-1/,exact,/about/,302,\n") || !strings.Contains(body, "/deleted-post/,exact,,410,") {
			t.Errorf("export: %d %s", r.code, body)
		}
		lines, errs, err := redirects.ReadCSV(strings.NewReader(body))
		if err != nil || len(errs) > 0 || len(lines) < 6 {
			t.Errorf("export doesn't read back: %d lines %v %v", len(lines), errs, err)
		}
	})

	t.Run("404 log", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO not_found_log (path, hits) VALUES ('/m2c-404-a/', 5), ('/m2c-404-b/', 9), ('/M2C-404-C', 1)
ON CONFLICT (path) DO UPDATE SET hits = EXCLUDED.hits`); err != nil {
			t.Fatal(err)
		}
		var page struct {
			Items []redirects.NotFound `json:"items"`
			Total int64                `json:"total"`
		}
		r := api.do(editor, "GET", "/not-found?q=m2c-404", "", "")
		r.json(t, &page)
		if r.code != 200 || page.Total != 3 || page.Items[0].Path != "/m2c-404-b/" {
			t.Fatalf("%d %s", r.code, r.body)
		}
		r = post("/not-found/dismiss", `{"paths":["/m2c-404-a/","/m2c-404-b/","/m2c-never/"]}`)
		var dismissed map[string]int64
		r.json(t, &dismissed)
		if r.code != 200 || dismissed["dismissed"] != 2 {
			t.Errorf("dismiss: %d %s", r.code, r.body)
		}
		if r := post("/redirects", `{"source":"/M2C-404-C","target":"/about/"}`); r.code != http.StatusCreated {
			t.Fatalf("%d %s", r.code, r.body)
		}
		r = api.do(editor, "GET", "/not-found?q=m2c-404", "", "")
		r.json(t, &page)
		if page.Total != 0 {
			t.Errorf("a new redirect clears its 404 row: %s", r.body)
		}
	})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
