package site

import (
  "context"
  "encoding/json"
  "fmt"
  "slices"
  "time"

  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgconn"
)

// Channel is the Postgres NOTIFY channel every replica listens on.
const Channel = "afsc_cache"

const (
  maxPaths   = 200
  maxPayload = 7900 // NOTIFY payloads must stay under 8000 bytes
)

// Event tells every replica what changed. Refresh reloads the site snapshot (settings, menus, pages, terms,
// authors, prefix and regex redirects) before purging. All purges every cached page; otherwise Paths lists groups.
type Event struct {
  Refresh bool     `json:"refresh,omitempty"`
  All     bool     `json:"all,omitempty"`
  Paths   []string `json:"paths,omitempty"`
}

// Merge combines two events, for example the URL sets before and after a slug change.
func (e Event) Merge(o Event) Event {
  m := Event{Refresh: e.Refresh || o.Refresh, All: e.All || o.All}
  if !m.All {
    m.Paths = slices.Concat(e.Paths, o.Paths)
  }
  return m.normalize()
}

func (e Event) normalize() Event {
  if e.All || len(e.Paths) > maxPaths {
    return Event{Refresh: e.Refresh, All: true}
  }
  paths := slices.Clone(e.Paths)
  slices.Sort(paths)
  e.Paths = slices.Compact(paths)
  return e
}

func (e Event) empty() bool { return !e.Refresh && !e.All && len(e.Paths) == 0 }

type execer interface {
  Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Notify broadcasts e to every listening replica. Used by CLI commands that change content.
func Notify(ctx context.Context, db execer, e Event) error {
  e = e.normalize()
  if e.empty() {
    return nil
  }
  b, err := json.Marshal(e)
  if err != nil {
    return err
  }
  if len(b) > maxPayload {
    b, _ = json.Marshal(Event{Refresh: e.Refresh, All: true})
  }
  if _, err := db.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, string(b)); err != nil {
    return fmt.Errorf("notify %s: %w", Channel, err)
  }
  return nil
}

// Invalidate applies e to this process, then broadcasts it. Replicas apply it again on receipt, which is harmless.
func (st *Site) Invalidate(ctx context.Context, e Event) error {
  st.apply(ctx, e)
  return Notify(ctx, st.db, e)
}

func (st *Site) apply(ctx context.Context, e Event) {
  e = e.normalize()
  if e.Refresh {
    // purge anyway on failure: the next poll brings the data, and pages must not outlive the change
    _ = st.refresh(ctx)
  }
  switch {
  case st.cache == nil:
  case e.All:
    st.cache.PurgeAll()
  case len(e.Paths) > 0:
    st.cache.Purge(e.Paths...)
  }
}

func (st *Site) refresh(ctx context.Context) error {
  st.refreshMu.Lock()
  defer st.refreshMu.Unlock()
  snap, err := st.loadSnapshot(ctx)
  if err != nil {
    st.cfg.Logf("site: refresh failed, keeping previous data: %v", err)
    return err
  }
  st.snap.Store(snap)
  return nil
}

// listen holds a dedicated connection with LISTEN afsc_cache, reconnecting with backoff until ctx is done.
func (st *Site) listen(ctx context.Context) {
  backoff, first := time.Second, true
  for {
    err := st.listenOnce(ctx, func() {
      backoff = time.Second
      if !first {
        // notifications sent while disconnected are lost
        st.apply(ctx, Event{Refresh: true, All: true})
      }
      first = false
    })
    st.listening.Store(false)
    if ctx.Err() != nil {
      return
    }
    st.cfg.Logf("site: cache listener: %v; reconnecting in %s", err, backoff)
    select {
    case <-ctx.Done():
      return
    case <-time.After(backoff):
    }
    backoff = min(2*backoff, 30*time.Second)
  }
}

func (st *Site) listenOnce(ctx context.Context, connected func()) error {
  conn, err := pgx.ConnectConfig(ctx, st.db.Config().ConnConfig.Copy())
  if err != nil {
    return err
  }
  defer func() {
    cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
    defer cancel()
    _ = conn.Close(cctx)
  }()
  if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
    return err
  }
  st.listening.Store(true)
  connected()
  for {
    n, err := conn.WaitForNotification(ctx)
    if err != nil {
      return err
    }
    var e Event
    if err := json.Unmarshal([]byte(n.Payload), &e); err != nil {
      st.cfg.Logf("site: bad cache event %q: %v", n.Payload, err)
      e = Event{Refresh: true, All: true}
    }
    st.apply(ctx, e)
  }
}
