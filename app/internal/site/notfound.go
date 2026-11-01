package site

import (
	"context"
	"regexp"
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

// noiseMatcher compiles seo.not_found.noise_patterns into one case-insensitive regexp over the whole raw path.
// In a pattern, * matches any run of characters (slashes too) and ? one character.
func noiseMatcher(patterns []string) *regexp.Regexp {
	if len(patterns) == 0 {
		return nil
	}
	alts := make([]string, len(patterns))
	for i, p := range patterns {
		var b strings.Builder
		for _, c := range p {
			switch c {
			case '*':
				b.WriteString(".*")
			case '?':
				b.WriteString(".")
			default:
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		alts[i] = b.String()
	}
	re, err := regexp.Compile(`(?is)^(?:` + strings.Join(alts, "|") + `)$`)
	if err != nil {
		return nil
	}
	return re
}

func (s *snapshot) noise404(path string) bool { return s.noise != nil && s.noise.MatchString(path) }

const (
	sqlRedirectHits = `
UPDATE redirects r SET hits = r.hits + u.n, last_hit_at = greatest(r.last_hit_at, u.last)
FROM unnest($1::bigint[], $2::bigint[], $3::timestamptz[]) AS u(id, n, last)
WHERE r.id = u.id`
	sqlPruneNotFound = `DELETE FROM not_found_log WHERE last_seen < now() - make_interval(days => $1)`
	pruneLockKey     = 0x6166736e66707275
)

type redirectHit struct {
	n    int64
	last time.Time
}

// redirectHits counts served redirects in memory; Site.Run writes them with the media hit counts. The map stays
// bounded because only existing redirect ids are counted.
type redirectHits struct {
	mu   sync.Mutex
	hits map[int64]redirectHit
}

func newRedirectHits() *redirectHits { return &redirectHits{hits: map[int64]redirectHit{}} }

func (h *redirectHits) hit(id int64, now time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	c := h.hits[id]
	c.n++
	c.last = now
	h.hits[id] = c
	h.mu.Unlock()
}

func (h *redirectHits) merge(batch map[int64]redirectHit) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, b := range batch {
		c := h.hits[id]
		c.n += b.n
		if b.last.After(c.last) {
			c.last = b.last
		}
		h.hits[id] = c
	}
}

// flushRedirectHits writes counted hits in one statement. On failure they are kept for the next flush.
func (st *Site) flushRedirectHits(ctx context.Context) {
	st.rh.mu.Lock()
	batch := st.rh.hits
	st.rh.hits = map[int64]redirectHit{}
	st.rh.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	ids, ns, lasts := make([]int64, 0, len(batch)), make([]int64, 0, len(batch)), make([]time.Time, 0, len(batch))
	for id, h := range batch {
		ids, ns, lasts = append(ids, id), append(ns, h.n), append(lasts, h.last)
	}
	if _, err := st.db.Exec(ctx, sqlRedirectHits, ids, ns, lasts); err != nil {
		st.cfg.Logf("site: flushing %d redirect hit counters failed: %v", len(batch), err)
		st.rh.merge(batch)
	}
}

// pruneNotFound deletes 404 log rows not seen within seo.not_found.retention_days. Replicas skip it while another
// one is pruning.
func (st *Site) pruneNotFound(ctx context.Context) {
	days := st.snap.Load().settings.SEO.NotFound.RetentionDays
	tx, err := st.db.Begin(ctx)
	if err != nil {
		st.cfg.Logf("site: prune not-found log: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(pruneLockKey)).Scan(&locked); err != nil || !locked {
		return
	}
	tag, err := tx.Exec(ctx, sqlPruneNotFound, days)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		st.cfg.Logf("site: prune not-found log: %v", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		st.cfg.Logf("site: pruned %d not-found log rows older than %d days", n, days)
	}
}
