package plugins

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/authz"
	"afsc/internal/jobs"
	"afsc/internal/theme"
)

// Host is what a plugin registers on during Init, and what it reads at run time.
type Host struct {
	id     string
	rt     *Runtime
	seq    *int
	errs   []error
	decls  []Setting
	assets map[string]asset // name -> file

	themeCheck func(json.RawMessage) error
}

func (h *Host) fail(format string, args ...any) { h.errs = append(h.errs, fmt.Errorf(format, args...)) }

// Key derives this plugin's 32-byte secret key from AFSC_JWT_SECRET. It fails where no secret is configured.
func (h *Host) Key() ([]byte, error) {
	if len(h.rt.cfg.Secret) < 32 {
		return nil, errors.New("AFSC_JWT_SECRET is not available")
	}
	return hkdf.Key(sha256.New, h.rt.cfg.Secret, nil, "afsc-"+h.id, 32)
}

func (h *Host) ID() string { return h.id }

// DB is the connection pool; nil outside `afsc serve`, `afsc worker` and tests that pass one.
func (h *Host) DB() *pgxpool.Pool { return h.rt.cfg.DB }

// BaseURL is AFSC_PUBLIC_BASE_URL, without a trailing slash.
func (h *Host) BaseURL() string { return h.rt.cfg.BaseURL }

func (h *Host) Logf(format string, args ...any) { h.rt.cfg.Logf(h.id+": "+format, args...) }

// Enabled reports whether this plugin is switched on right now.
func (h *Host) Enabled() bool { return h.rt.Enabled(h.id) }

var knownHooks = map[string]bool{}

func init() {
	for _, n := range FilterNames {
		knownHooks[n] = true
	}
}

func (h *Host) addHandler(name string, prio int, fn any) {
	if !knownHooks[name] {
		h.fail("unknown hook %q", name)
		return
	}
	*h.seq++
	h.rt.filters[name] = append(h.rt.filters[name], handler{plugin: h.id, prio: prio, seq: *h.seq, fn: fn})
}

var funcNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RegisterTemplateFuncs adds funcs for themes. Every name must start with plg_<id>_. While the plugin is off, or
// if a func panics, it returns zero values, so templates that call it still render.
func (h *Host) RegisterTemplateFuncs(fm template.FuncMap) {
	prefix := "plg_" + h.id + "_"
	for name, fn := range fm {
		v := reflect.ValueOf(fn)
		switch {
		case !strings.HasPrefix(name, prefix) || len(name) == len(prefix) || !funcNameRe.MatchString(name):
			h.fail("template func %q must be named %s<name>", name, prefix)
		case v.Kind() != reflect.Func:
			h.fail("template func %q is not a func", name)
		case h.rt.funcs[name] != nil:
			h.fail("template func %q is registered twice", name)
		default:
			h.rt.funcs[name] = h.guard(name, v)
		}
	}
}

func (h *Host) guard(name string, v reflect.Value) any {
	t := v.Type()
	zero := func() []reflect.Value {
		out := make([]reflect.Value, t.NumOut())
		for i := range out {
			out[i] = reflect.Zero(t.Out(i))
		}
		return out
	}
	return reflect.MakeFunc(t, func(args []reflect.Value) (out []reflect.Value) {
		if !h.Enabled() {
			return zero()
		}
		defer func() {
			if r := recover(); r != nil {
				h.rt.logFailure(h.id, name, fmt.Errorf("panic: %v", r))
				out = zero()
			}
		}()
		if t.IsVariadic() {
			return v.CallSlice(args)
		}
		return v.Call(args)
	}).Interface()
}

var adminSubpathRe = regexp.MustCompile(`^[a-z0-9:][a-z0-9:_/-]{0,127}$`)

var subpathRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)

// RegisterPublicRoute serves handler at /_plugins/<id>/<subpath> for GET (and HEAD) or POST. Responses are never
// stored in the page cache; they get the public security headers.
func (h *Host) RegisterPublicRoute(method, subpath string, handler http.Handler) {
	if method != http.MethodGet && method != http.MethodPost {
		h.fail("public route %s %s: only GET and POST are supported", method, subpath)
		return
	}
	if !subpathRe.MatchString(subpath) || strings.Contains(subpath, "..") || strings.HasPrefix(subpath, "assets/") {
		h.fail("public route %q: subpath must match %s and not start with assets/", subpath, subpathRe)
		return
	}
	key := method + " " + PublicPrefix + h.id + "/" + subpath
	if h.rt.public[key] != nil {
		h.fail("public route %s is registered twice", key)
		return
	}
	h.rt.public[key] = &publicRoute{plugin: h.id, handler: handler}
}

// RegisterAdminAPI serves handler at /admin/api/v1/plugins/<id>/<subpath>. cap is required (Public isn't allowed);
// the admin's auth, CSRF and capability checks run first. The subpath may contain :params (read with PathParam).
func (h *Host) RegisterAdminAPI(method, subpath string, cp authz.Cap, handler http.Handler) {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		h.fail("admin route %s %s: unsupported method", method, subpath)
		return
	}
	if cp == authz.Public {
		h.fail("admin route %s %s: a capability is required", method, subpath)
		return
	}
	if subpath != "" && !adminSubpathRe.MatchString(subpath) {
		h.fail("admin route %q: invalid subpath", subpath)
		return
	}
	path := "/plugins/" + h.id
	if subpath != "" {
		path += "/" + subpath
	}
	for _, r := range h.rt.admin {
		if r.Method == method && r.Path == path {
			h.fail("admin route %s %s is registered twice", method, path)
			return
		}
	}
	h.rt.admin = append(h.rt.admin, AdminRoute{Plugin: h.id, Method: method, Path: path, Cap: cp, Handler: handler})
}

// RegisterJob handles a job kind, which must start with "<id>.". The handler no-ops while the plugin is off.
func (h *Host) RegisterJob(kind string, handler jobs.Handler, o jobs.Options) {
	if !strings.HasPrefix(kind, h.id+".") || len(kind) == len(h.id)+1 {
		h.fail("job kind %q must start with %q", kind, h.id+".")
		return
	}
	if _, dup := h.rt.jobs[kind]; dup {
		h.fail("job kind %q is registered twice", kind)
		return
	}
	h.rt.jobs[kind] = Job{Plugin: h.id, Handler: handler, Options: o}
}

// RegisterSchedule enqueues one of this plugin's job kinds on an interval from the scheduler leader.
func (h *Host) RegisterSchedule(s jobs.Schedule) {
	if j, ok := h.rt.jobs[s.Kind]; !ok || j.Plugin != h.id {
		h.fail("schedule %q: register the job kind first", s.Kind)
		return
	}
	if s.Every <= 0 {
		h.fail("schedule %q: Every must be positive", s.Kind)
		return
	}
	h.rt.schedules = append(h.rt.schedules, s)
}

// RegisterMigrations adds golang-migrate files (NNNN_name.up.sql / .down.sql at the root of fsys). Every table,
// index, sequence and type they create, alter or drop must be named plg_<id>_….
func (h *Host) RegisterMigrations(fsys fs.FS) {
	for _, m := range h.rt.migs {
		if m.Plugin == h.id {
			h.fail("migrations registered twice")
			return
		}
	}
	if err := CheckMigrations(h.id, fsys); err != nil {
		h.errs = append(h.errs, err)
		return
	}
	h.rt.migs = append(h.rt.migs, Migrations{Plugin: h.id, FS: fsys})
}

// Setting is one plugin option, edited under Admin → Settings → Plugins. Type is one of theme.SettingTypes.
type Setting struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Help     string   `json:"help,omitempty"`
	Type     string   `json:"type"`
	Default  any      `json:"default"`
	Options  []string `json:"options,omitempty"`
	Min, Max *float64 `json:"-"`
}

func (s Setting) MarshalJSON() ([]byte, error) {
	type plain Setting
	return json.Marshal(struct {
		plain
		Min *float64 `json:"min,omitempty"`
		Max *float64 `json:"max,omitempty"`
	}{plain(s), s.Min, s.Max})
}

// Check validates a JSON-decoded value.
func (s Setting) Check(v any) error {
	if err := (theme.Setting{Type: s.Type, Options: s.Options}).Check(v); err != nil {
		return err
	}
	if n, ok := v.(float64); ok {
		if s.Min != nil && n < *s.Min {
			return fmt.Errorf("must be at least %v", *s.Min)
		}
		if s.Max != nil && n > *s.Max {
			return fmt.Errorf("must be at most %v", *s.Max)
		}
	}
	return nil
}

var settingKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// RegisterSettings declares this plugin's options. Defaults must pass their own validation.
func (h *Host) RegisterSettings(fields ...Setting) {
	for _, f := range fields {
		if !settingKeyRe.MatchString(f.Key) || slices.ContainsFunc(h.decls, func(d Setting) bool { return d.Key == f.Key }) {
			h.fail("setting key %q is invalid or duplicated", f.Key)
			continue
		}
		b, err := json.Marshal(f.Default)
		if err == nil {
			err = json.Unmarshal(b, &f.Default)
		}
		if err == nil {
			err = f.Check(f.Default)
		}
		if err != nil {
			h.fail("setting %q: default %v: %v", f.Key, f.Default, err)
			continue
		}
		h.decls = append(h.decls, f)
	}
}

// Settings returns this plugin's current values, with defaults for anything not saved.
func (h *Host) Settings() map[string]any {
	saved := h.rt.current().Plugins.Settings[h.id]
	out := make(map[string]any, len(h.decls))
	for _, d := range h.decls {
		out[d.Key] = d.Default
		if v, ok := saved[d.Key]; ok && d.Check(v) == nil {
			out[d.Key] = v
		}
	}
	return out
}

func (h *Host) Bool(key string) bool {
	v, _ := h.Settings()[key].(bool)
	return v
}

func (h *Host) String(key string) string {
	v, _ := h.Settings()[key].(string)
	return v
}

func (h *Host) Number(key string) float64 {
	v, _ := h.Settings()[key].(float64)
	return v
}
