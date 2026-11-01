package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"afsc/internal/content"
	"afsc/internal/media"
	"afsc/internal/storage"
	"afsc/internal/theme"
)

// mediaFakeStorage keeps objects in memory; it implements storage.Manager and storage.Reader.
type mediaFakeStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (f *mediaFakeStorage) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, ct string) error {
	return f.PutStream(ctx, key, body, size, ct)
}

func (f *mediaFakeStorage) PutStream(_ context.Context, key string, body io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = b
	return nil
}

func (f *mediaFakeStorage) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

func (f *mediaFakeStorage) Get(_ context.Context, key, _ string) (*storage.Object, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	n := int64(len(b))
	return &storage.Object{Body: io.NopCloser(bytes.NewReader(b)), Size: n, Length: n}, nil
}

func (f *mediaFakeStorage) Head(ctx context.Context, key string) (*storage.Object, error) {
	o, err := f.Get(ctx, key, "")
	if o != nil {
		o.Body = nil
	}
	return o, err
}

func (f *mediaFakeStorage) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

// mediaEnv swaps in the fake storage and deletes the media rows the test creates.
func mediaEnv(t *testing.T) (*adminEnv, *mediaFakeStorage, *[]int64) {
	env := newAdminEnv(t)
	fake := &mediaFakeStorage{objects: map[string][]byte{}}
	env.deps.storage = fake
	ids := &[]int64{}
	t.Cleanup(func() {
		if _, err := env.pool.Exec(context.Background(), `DELETE FROM media WHERE id = ANY($1::bigint[])`, *ids); err != nil {
			t.Error(err)
		}
	})
	return env, fake, ids
}

// mediaPNG is a w×h PNG; salt goes after IEND so every test run uploads a file the library hasn't seen.
func mediaPNG(t *testing.T, w, h int, salt string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return append(b.Bytes(), salt...)
}

func mediaSend(env *adminEnv, role, method, path, filename string, body []byte, mod ...func(*http.Request)) apiResp {
	env.t.Helper()
	req := httptest.NewRequest(method, apiPrefix+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Filename", url.PathEscape(filename))
	req.Header.Set("Authorization", "Bearer "+env.tokens[role])
	req.Header.Set("Origin", testOrigin)
	for _, m := range mod {
		m(req)
	}
	w := httptest.NewRecorder()
	env.api.e.ServeHTTP(w, req)
	return apiResp{code: w.Code, body: w.Body.Bytes(), hdr: w.Header()}
}

func mediaUploaded(t *testing.T, r apiResp, want int, ids *[]int64) media.Item {
	t.Helper()
	if r.code != want {
		t.Fatalf("upload: %d %s", r.code, r.body)
	}
	var it media.Item
	r.json(t, &it)
	*ids = append(*ids, it.ID)
	return it
}

func mediaServer(t *testing.T, env *adminEnv, fake *mediaFakeStorage) *media.Server {
	tm, err := theme.LoadManifest("themes/default")
	if err != nil {
		t.Fatal(err)
	}
	return media.New(media.Config{Store: &media.PG{DB: env.pool}, Storage: fake, Images: func() *content.ImageSizes { return tm.Images() }})
}

func mediaKey(t *testing.T, env *adminEnv, id int64) string {
	var key string
	if err := env.pool.QueryRow(context.Background(), `SELECT storage_key FROM media WHERE id = $1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestMediaUpload(t *testing.T) {
	env, fake, ids := mediaEnv(t)
	body := mediaPNG(t, 5, 4, env.suffix)

	// the name says JPEG, the bytes say PNG: the bytes win
	it := mediaUploaded(t, mediaSend(env, "author", http.MethodPost, "/media", "photo one.jpg", body), http.StatusCreated, ids)
	if it.Mime != "image/png" || it.Filename != "photo one.png" || it.Width == nil || *it.Width != 5 || *it.Height != 4 {
		t.Errorf("item: %+v", it)
	}
	if !strings.HasPrefix(it.URL, fmt.Sprintf("/m/%d/", it.ID)) || it.ThumbURL == nil {
		t.Errorf("urls: %s %v", it.URL, it.ThumbURL)
	}
	if it.UploadedBy == nil || *it.UploadedBy != env.ids["author"].String() {
		t.Errorf("uploaded_by: %v", it.UploadedBy)
	}
	if n := len(fake.objects); n != 1 {
		t.Fatalf("%d objects stored", n)
	}

	// the same bytes again: the existing item, and the second copy is removed from storage
	dup := mediaUploaded(t, mediaSend(env, "editor", http.MethodPost, "/media", "again.png", body), http.StatusOK, ids)
	if dup.ID != it.ID || len(fake.objects) != 1 {
		t.Errorf("duplicate: id %d vs %d, %d objects", dup.ID, it.ID, len(fake.objects))
	}
	var audits int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'media.upload' AND entity_id = $1`,
		fmt.Sprint(it.ID)).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("audit rows: %d %v", audits, err)
	}

	cases := []struct {
		name, role, file string
		body             []byte
		mod              func(*http.Request)
		want             int
		code             string
	}{
		{"contributor", "contributor", "a.png", body, nil, http.StatusForbidden, "capability"},
		{"html", "admin", "a.html", []byte("<!doctype html><title>x</title>" + env.suffix), nil, http.StatusUnsupportedMediaType, "type"},
		{"svg as editor", "editor", "a.svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), nil, http.StatusForbidden, "capability"},
		{"over the limit", "admin", "big.png", body, func(r *http.Request) { r.ContentLength = 21 << 20 }, http.StatusRequestEntityTooLarge, "too_large"},
		{"no length", "admin", "a.png", body, func(r *http.Request) { r.ContentLength = -1 }, http.StatusLengthRequired, ""},
	}
	for _, c := range cases {
		var mods []func(*http.Request)
		if c.mod != nil {
			mods = append(mods, c.mod)
		}
		r := mediaSend(env, c.role, http.MethodPost, "/media", c.file, c.body, mods...)
		if r.code != c.want || !strings.Contains(string(r.body), c.code) {
			t.Errorf("%s: %d %s", c.name, r.code, r.body)
		}
	}
	if len(fake.objects) != 1 {
		t.Errorf("refused uploads left %d objects", len(fake.objects))
	}

	// admins may upload SVG, and it is still served sandboxed
	svg := mediaUploaded(t, mediaSend(env, "admin", http.MethodPost, "/media", "logo.svg",
		[]byte("<svg xmlns='http://www.w3.org/2000/svg'><!--"+env.suffix+"--></svg>")), http.StatusCreated, ids)
	if svg.Mime != "image/svg+xml" {
		t.Errorf("svg mime %s", svg.Mime)
	}
	w := httptest.NewRecorder()
	mediaServer(t, env, fake).ServeMedia(w, httptest.NewRequest(http.MethodGet, svg.URL, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("svg served: %d %v", w.Code, w.Header())
	}
}

func TestMediaReplaceAndDelete(t *testing.T) {
	env, fake, ids := mediaEnv(t)
	importSample(t, env.pool)
	ctx := context.Background()
	it := mediaUploaded(t, mediaSend(env, "author", http.MethodPost, "/media", "chart.png", mediaPNG(t, 8, 6, env.suffix)),
		http.StatusCreated, ids)
	oldKey := mediaKey(t, env, it.ID)
	legacy := "/wp-content/uploads/2020/01/chart-" + env.suffix + ".png"
	if _, err := env.pool.Exec(ctx, `INSERT INTO media_legacy_paths (path, media_id, variant, hits) VALUES ($1, $2, 'orig', 7)`,
		legacy, it.ID); err != nil {
		t.Fatal(err)
	}
	var postID int64
	if err := env.pool.QueryRow(ctx, `SELECT id FROM posts WHERE status = 'published' ORDER BY id LIMIT 1`).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO post_media (post_id, media_id) VALUES ($1, $2)`, postID, it.ID); err != nil {
		t.Fatal(err)
	}

	var d media.Detail
	r := env.do("contributor", http.MethodGet, fmt.Sprintf("/media/%d", it.ID), "")
	if r.code != http.StatusOK {
		t.Fatalf("get: %d %s", r.code, r.body)
	}
	r.json(t, &d)
	if len(d.UsedIn) != 1 || d.UsedIn[0].ID != postID || len(d.LegacyPaths) != 1 || d.LegacyPaths[0].Hits != 7 {
		t.Errorf("detail: used_in %+v, legacy %+v", d.UsedIn, d.LegacyPaths)
	}

	// only the uploader or an editor may change it
	if r := env.do("admin", http.MethodPatch, fmt.Sprintf("/media/%d", it.ID), `{"alt":"A chart","focal_x":0.25,"focal_y":0.75}`); r.code != http.StatusOK {
		t.Errorf("patch as admin: %d %s", r.code, r.body)
	}
	adminIt := mediaUploaded(t, mediaSend(env, "admin", http.MethodPost, "/media", "admin.png", mediaPNG(t, 2, 2, env.suffix)),
		http.StatusCreated, ids)
	if r := env.do("author", http.MethodPatch, fmt.Sprintf("/media/%d", adminIt.ID), `{"alt":"x"}`); r.code != http.StatusForbidden ||
		!strings.Contains(string(r.body), "ownership") {
		t.Errorf("patch someone else's upload: %d %s", r.code, r.body)
	}

	path := fmt.Sprintf("/media/%d/file", it.ID)
	newBody := mediaPNG(t, 16, 12, env.suffix)
	r = mediaSend(env, "author", http.MethodPut, path, "chart-v2.png", newBody)
	if r.code != http.StatusConflict || !strings.Contains(string(r.body), `"legacy_paths"`) || !strings.Contains(string(r.body), legacy) {
		t.Fatalf("replace without confirm: %d %s", r.code, r.body)
	}
	if !fake.has(oldKey) || len(fake.objects) != 2 {
		t.Errorf("refused replace touched storage: %d objects", len(fake.objects))
	}
	r = mediaSend(env, "author", http.MethodPut, path+"?confirm=1", "chart-v2.png", newBody)
	if r.code != http.StatusOK {
		t.Fatalf("replace: %d %s", r.code, r.body)
	}
	var rep media.Item
	r.json(t, &rep)
	if rep.ID != it.ID || rep.URL == it.URL || *rep.Width != 16 || rep.Alt != "A chart" {
		t.Errorf("replaced: %+v (was %s)", rep, it.URL)
	}
	if fake.has(oldKey) || !fake.has(mediaKey(t, env, it.ID)) {
		t.Error("old object kept or new one missing")
	}
	w := httptest.NewRecorder()
	mediaServer(t, env, fake).ServeMedia(w, httptest.NewRequest(http.MethodGet, it.URL, nil))
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != rep.URL {
		t.Errorf("old version: %d → %q, want %s", w.Code, w.Header().Get("Location"), rep.URL)
	}

	del := fmt.Sprintf("/media/%d", it.ID)
	if r := env.do("author", http.MethodDelete, del+"?confirm=1", ""); r.code != http.StatusForbidden {
		t.Errorf("delete as author: %d", r.code)
	}
	if r := env.do("editor", http.MethodDelete, del, ""); r.code != http.StatusConflict || !strings.Contains(string(r.body), "legacy_paths") {
		t.Errorf("delete without confirm: %d %s", r.code, r.body)
	}
	newKey := mediaKey(t, env, it.ID)
	if r := env.do("editor", http.MethodDelete, del+"?confirm=1", ""); r.code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.code, r.body)
	}
	if r := env.do("editor", http.MethodGet, del, ""); r.code != http.StatusNotFound {
		t.Errorf("get after delete: %d", r.code)
	}
	if fake.has(newKey) {
		t.Error("deleted media's object is still stored")
	}

	if r := env.do("editor", http.MethodGet, "/media/legacy", ""); r.code != http.StatusOK {
		t.Errorf("legacy list: %d %s", r.code, r.body)
	}
	if r := env.do("author", http.MethodGet, "/media?type=image&q=admin", ""); r.code != http.StatusOK ||
		!strings.Contains(string(r.body), fmt.Sprintf(`"id":%d`, adminIt.ID)) {
		t.Errorf("list: %d %s", r.code, r.body)
	}
}
