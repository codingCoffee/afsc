package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/jobs"
	"afsc/internal/metrics"
	"afsc/internal/settings"
)

// Config is what plugins can reach from the host. DB is nil in `afsc theme check` and `afsc migrate`.
type Config struct {
	DB      *pgxpool.Pool
	BaseURL string
	Secret  []byte // AFSC_JWT_SECRET, for Host.Key; nil where it isn't needed
	Logf    func(format string, args ...any)
	// Fixtures is set by `afsc theme check`; see Host.Fixtures.
	Fixtures bool
}

type entry struct {
	plugin Plugin
	info   Info
	host   *Host
}

// Runtime is the built registry. All methods are safe on a nil *Runtime, which behaves as "no plugins".
type Runtime struct {
	cfg       Config
	entries   []*entry
	byID      map[string]*entry
	filters   map[string][]handler
	actions   map[string][]action
	funcs     template.FuncMap
	public    map[string]*publicRoute
	admin     []AdminRoute
	jobs      map[string]Job
	schedules []jobs.Schedule
	migs      []Migrations

	importJobs []string

	settings atomic.Pointer[func() *settings.All]
	backend  atomic.Pointer[Backend]

	logMu  sync.Mutex
	logged map[string]time.Time
}

// Build runs every plugin's Init and validates what it registered. Any violation is an error: the process must
// not start with a plugin that could shadow routes, collide on names or write outside its tables.
func Build(ctx context.Context, cfg Config, ps []Plugin) (*Runtime, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	rt := &Runtime{cfg: cfg, byID: map[string]*entry{}, filters: map[string][]handler{}, actions: map[string][]action{},
		funcs:  template.FuncMap{},
		public: map[string]*publicRoute{}, jobs: map[string]Job{}, logged: map[string]time.Time{}}
	var errs []error
	seq := 0
	for _, p := range ps {
		id := p.ID()
		if !IDPattern.MatchString(id) {
			errs = append(errs, fmt.Errorf("plugin id %q must match %s", id, IDPattern))
			continue
		}
		if rt.byID[id] != nil {
			errs = append(errs, fmt.Errorf("plugin %q is registered twice", id))
			continue
		}
		h := &Host{id: id, rt: rt, seq: &seq}
		e := &entry{plugin: p, info: info(p), host: h}
		rt.entries = append(rt.entries, e)
		rt.byID[id] = e
		if err := p.Init(ctx, h); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: init: %w", id, err))
		}
		for _, err := range h.errs {
			errs = append(errs, fmt.Errorf("plugin %s: %w", id, err))
		}
	}
	for name, hs := range rt.filters {
		slices.SortStableFunc(hs, func(a, b handler) int {
			if a.prio != b.prio {
				return a.prio - b.prio
			}
			return a.seq - b.seq
		})
		rt.filters[name] = hs
	}
	for _, as := range rt.actions {
		slices.SortStableFunc(as, func(a, b action) int {
			if a.prio != b.prio {
				return a.prio - b.prio
			}
			return a.seq - b.seq
		})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return rt, nil
}

// Bind gives the runtime the live settings. Until then (and in theme check) every plugin uses its defaults.
func (rt *Runtime) Bind(s func() *settings.All) {
	if rt != nil {
		rt.settings.Store(&s)
	}
}

func (rt *Runtime) current() *settings.All {
	if f := rt.settings.Load(); f != nil {
		return (*f)()
	}
	d := settings.Defaults()
	return &d
}

// Enabled reports whether plugin id is switched on in the current settings.
func (rt *Runtime) Enabled(id string) bool {
	if rt == nil {
		return false
	}
	e := rt.byID[id]
	if e == nil {
		return false
	}
	if e.info.AlwaysOn {
		return true
	}
	if v, ok := rt.current().Plugins.Enabled[id]; ok {
		return v
	}
	return e.info.DefaultEnabled
}

// Funcs is every plugin template func, wrapped to return zero values while its plugin is off or after a panic.
func (rt *Runtime) Funcs() template.FuncMap {
	if rt == nil {
		return nil
	}
	return rt.funcs
}

// Job is a job handler a plugin registered.
type Job struct {
	Plugin  string
	Handler jobs.Handler
	Options jobs.Options
}

// Jobs maps job kinds to handlers that no-op while their plugin is off.
func (rt *Runtime) Jobs() map[string]Job {
	out := map[string]Job{}
	if rt == nil {
		return out
	}
	for kind, j := range rt.jobs {
		h := j.Handler
		j.Handler = func(ctx context.Context, p json.RawMessage) error {
			if !rt.Enabled(j.Plugin) {
				return nil
			}
			return h(ctx, p)
		}
		out[kind] = j
	}
	if len(rt.actions) > 0 {
		out[ActionKind] = Job{Handler: rt.runAction}
	}
	return out
}

func (rt *Runtime) Schedules() []jobs.Schedule {
	if rt == nil {
		return nil
	}
	return rt.schedules
}

// Migrations is one plugin's golang-migrate files, applied with their own version table.
type Migrations struct {
	Plugin string
	FS     fs.FS
}

// VersionTable is where golang-migrate records a plugin's schema version.
func (m Migrations) VersionTable() string { return "plg_" + m.Plugin + "_schema_migrations" }

func (rt *Runtime) Migrations() []Migrations {
	if rt == nil {
		return nil
	}
	return rt.migs
}

// call runs fn with a timeout and turns a panic into an error. A handler that overruns keeps running in its
// goroutine with a cancelled context; its result is dropped.
func call[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	type out struct {
		v   T
		err error
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan out, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- out{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		v, err := fn(cctx)
		ch <- out{v, err}
	}()
	select {
	case o := <-ch:
		return o.v, o.err
	case <-cctx.Done():
		var zero T
		return zero, fmt.Errorf("timed out after %s", timeout)
	}
}

// logFailure logs a failed hook call at most once a minute per plugin and hook.
func (rt *Runtime) logFailure(plugin, hook string, err error) {
	metrics.PluginFailures.Add(hook, 1)
	key := plugin + " " + hook
	rt.logMu.Lock()
	last, seen := rt.logged[key]
	now := time.Now()
	if seen && now.Sub(last) < time.Minute {
		rt.logMu.Unlock()
		return
	}
	rt.logged[key] = now
	rt.logMu.Unlock()
	rt.cfg.Logf("plugins: %s: %s failed: %v", plugin, hook, err)
}
