package site

import (
  "context"
  "strings"
  "sync"
  "time"
)

const (
  nfMaxPending = 10000 // distinct paths held between flushes; more are dropped
  nfFlushAt    = 1000
  nfMaxPath    = 2048
  nfMaxRef     = 1024
)

const sqlNotFound = `
INSERT INTO not_found_log AS n (path, hits, first_seen, last_seen, last_referrer)
SELECT * FROM unnest($1::text[], $2::bigint[], $3::timestamptz[], $4::timestamptz[], $5::text[])
ON CONFLICT (path) DO UPDATE SET
  hits = n.hits + EXCLUDED.hits,
  last_seen = greatest(n.last_seen, EXCLUDED.last_seen),
  last_referrer = CASE WHEN EXCLUDED.last_referrer <> '' THEN EXCLUDED.last_referrer ELSE n.last_referrer END`

type nfHit struct {
  hits        int64
  first, last time.Time
  ref         string
}

// notFoundLog aggregates 404s in memory; Site.Run writes them in one statement per flush.
type notFoundLog struct {
  mu      sync.Mutex
  pending map[string]*nfHit
  full    chan struct{}
}

func newNotFoundLog() *notFoundLog {
  return &notFoundLog{pending: map[string]*nfHit{}, full: make(chan struct{}, 1)}
}

func clean(s string, limit int) string {
  if len(s) > limit {
    s = s[:limit]
  }
  return strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
}

func (l *notFoundLog) add(path, ref string, now time.Time) {
  if len(path) > nfMaxPath {
    return
  }
  path, ref = clean(path, nfMaxPath), clean(ref, nfMaxRef)
  l.mu.Lock()
  defer l.mu.Unlock()
  h := l.pending[path]
  if h == nil {
    if len(l.pending) >= nfMaxPending {
      return
    }
    h = &nfHit{first: now}
    l.pending[path] = h
  }
  h.hits++
  h.last = now
  if ref != "" {
    h.ref = ref
  }
  if len(l.pending) >= nfFlushAt {
    select {
    case l.full <- struct{}{}:
    default:
    }
  }
}

func (l *notFoundLog) take() map[string]*nfHit {
  l.mu.Lock()
  defer l.mu.Unlock()
  p := l.pending
  l.pending = map[string]*nfHit{}
  return p
}

func (st *Site) flushNotFound(ctx context.Context) {
  batch := st.nf.take()
  if len(batch) == 0 {
    return
  }
  var paths, refs []string
  var hits []int64
  var firsts, lasts []time.Time
  for p, h := range batch {
    paths, hits, firsts, lasts, refs = append(paths, p), append(hits, h.hits), append(firsts, h.first), append(lasts, h.last), append(refs, h.ref)
  }
  if _, err := st.db.Exec(ctx, sqlNotFound, paths, hits, firsts, lasts, refs); err != nil {
    st.cfg.Logf("site: dropped %d not-found log entries: %v", len(batch), err)
  }
}
