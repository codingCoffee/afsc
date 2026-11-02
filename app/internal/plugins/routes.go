package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"afsc/internal/authz"
)

// PublicPrefix is where plugin public routes and assets live. Nothing else may be served under it.
const PublicPrefix = "/_plugins/"

const maxAssetBytes = 2 << 20

type publicRoute struct {
	plugin  string
	handler http.Handler
}

type asset struct {
	hash string
	body []byte
}

// RegisterAssets serves every file in fsys at /_plugins/<id>/assets/<hash>/<name>, cached for a year. Use
// AssetURL in hooks to link them; a stale hash still gets the current file, briefly cached.
func (h *Host) RegisterAssets(fsys fs.FS) {
	if h.assets != nil {
		h.fail("assets registered twice")
		return
	}
	h.assets = map[string]asset{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if len(b) > maxAssetBytes {
			return fmt.Errorf("asset %s is over 2 MB", p)
		}
		sum := sha256.Sum256(b)
		h.assets[p] = asset{hash: hex.EncodeToString(sum[:])[:12], body: b}
		return nil
	})
	if err != nil {
		h.fail("assets: %v", err)
	}
}

// AssetURL is the root-relative, content-hashed URL of a registered asset. It panics on an unknown name, which
// a plugin's own tests catch.
func (h *Host) AssetURL(name string) string {
	a, ok := h.assets[name]
	if !ok {
		panic("plugins: " + h.id + ": unknown asset " + name)
	}
	return PublicPrefix + h.id + "/assets/" + a.hash + "/" + (&url.URL{Path: name}).EscapedPath()
}

// ServePublic answers /_plugins/… requests: assets, then registered routes. A disabled plugin's paths are 404.
func (rt *Runtime) ServePublic(w http.ResponseWriter, r *http.Request) {
	if rt == nil {
		http.NotFound(w, r)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, PublicPrefix)
	id, sub, _ := strings.Cut(rest, "/")
	e := rt.byID[id]
	if !ok || e == nil || !rt.Enabled(id) {
		http.NotFound(w, r)
		return
	}
	if after, isAsset := strings.CutPrefix(sub, "assets/"); isAsset {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		hash, name, _ := strings.Cut(after, "/")
		a, ok := e.host.assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if hash == a.hash {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=60")
		}
		if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.body))
		return
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	route := rt.public[method+" "+r.URL.Path]
	if route == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	route.handler.ServeHTTP(w, r)
}

// AdminRoute is a plugin admin API route. Path is relative to /admin/api/v1.
type AdminRoute struct {
	Plugin  string
	Method  string
	Path    string
	Cap     authz.Cap
	Handler http.Handler
}

func (rt *Runtime) AdminRoutes() []AdminRoute {
	if rt == nil {
		return nil
	}
	return rt.admin
}

// Admin is the signed-in user behind an admin API request, and the route's path params.
type Admin struct {
	User   uuid.UUID
	Role   string
	IP     string
	Params map[string]string
}

type adminKey struct{}

// WithAdmin is called by the admin mount before a plugin handler runs.
func WithAdmin(ctx context.Context, a Admin) context.Context {
	return context.WithValue(ctx, adminKey{}, a)
}

// AdminFrom returns the admin request's user; ok is false outside RegisterAdminAPI handlers.
func AdminFrom(ctx context.Context) (Admin, bool) {
	a, ok := ctx.Value(adminKey{}).(Admin)
	return a, ok
}

// PathParam reads a :param from a RegisterAdminAPI route.
func PathParam(r *http.Request, name string) string {
	a, _ := AdminFrom(r.Context())
	return a.Params[name]
}

// JSON writes v with status, the way core admin handlers do.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes {"message": msg}, plus "field" when set, matching core admin errors.
func Error(w http.ResponseWriter, status int, field, msg string) {
	body := map[string]string{"message": msg}
	if field != "" {
		body["field"] = field
	}
	JSON(w, status, body)
}
