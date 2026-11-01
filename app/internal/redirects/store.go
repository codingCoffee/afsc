package redirects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("redirect not found")

// lockKey serialises redirect writes, so two saves can't build a chain between them.
const lockKey = 0x6166736372656469

// Store reads and writes the redirects and not_found_log tables.
type Store struct {
	DB     *pgxpool.Pool
	Router Router
}

// Purge is what a change means for cached pages: exact sources to purge, or Refresh for prefix and regex changes,
// which live in the site snapshot (refresh and purge everything).
type Purge struct {
	Paths   []string
	Refresh bool
}

// Row is a redirect as the admin API shows it.
type Row struct {
	ID        int64      `json:"id"`
	Source    string     `json:"source"`
	Match     string     `json:"match"`
	Target    *string    `json:"target"`
	Status    int        `json:"status"`
	Hits      int64      `json:"hits"`
	LastHitAt *time.Time `json:"last_hit_at"`
	Note      string     `json:"note"`
	Origin    string     `json:"origin"`
	LegacyKey *string    `json:"legacy_key"`
	CreatedBy *string    `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

const rowCols = `id, source, match, target, status, hits, last_hit_at, note, origin, legacy_key, created_by::text, created_at, updated_at`

func scanRow(row pgx.Row) (*Row, error) {
	r := &Row{}
	err := row.Scan(&r.ID, &r.Source, &r.Match, &r.Target, &r.Status, &r.Hits, &r.LastHitAt, &r.Note, &r.Origin, &r.LegacyKey,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) Get(ctx context.Context, id int64) (*Row, error) {
	return scanRow(s.DB.QueryRow(ctx, `SELECT `+rowCols+` FROM redirects WHERE id = $1`, id))
}

// ListQuery filters and pages the redirect list. Sort is hits, source or created (the default).
type ListQuery struct {
	Q, Match, Origin, Sort string
	Page, Size             int
}

var listOrder = map[string]string{
	"hits":    "hits DESC, id",
	"source":  "source, id",
	"created": "created_at DESC, id DESC",
}

func likePattern(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

func (s *Store) List(ctx context.Context, q ListQuery) ([]Row, int64, error) {
	order, ok := listOrder[q.Sort]
	if !ok {
		order = listOrder["created"]
	}
	where := `($1 = '' OR source ILIKE $2 OR coalesce(target, '') ILIKE $2 OR note ILIKE $2)
  AND ($3 = '' OR match = $3) AND ($4 = '' OR origin = $4)`
	args := []any{q.Q, likePattern(q.Q), q.Match, q.Origin}
	var total int64
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM redirects WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+rowCols+` FROM redirects WHERE `+where+` ORDER BY `+order+` LIMIT $5 OFFSET $6`,
		append(args, q.Size, (q.Page-1)*q.Size)...)
	if err != nil {
		return nil, 0, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Row, error) {
		r, err := scanRow(row)
		if err != nil {
			return Row{}, err
		}
		return *r, nil
	})
	return out, total, err
}

// Export writes every redirect as CSV, in id order.
func (s *Store) Export(ctx context.Context, w io.Writer) error {
	rules, err := loadRules(ctx, s.DB)
	if err != nil {
		return err
	}
	return WriteCSV(w, rules)
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadRules(ctx context.Context, db querier) ([]*Rule, error) {
	rows, err := db.Query(ctx, `SELECT id, source, match, coalesce(target, ''), status, note FROM redirects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Rule
	for rows.Next() {
		r := &Rule{}
		if err := rows.Scan(&r.ID, &r.Source, &r.Match, &r.Target, &r.Status, &r.Note); err != nil {
			return nil, err
		}
		_ = r.Compile() // a bad imported regex never matches but stays editable
		out = append(out, r)
	}
	return out, rows.Err()
}

// batch is a set of changes to the in-memory table, written in one transaction.
type batch struct {
	ctx context.Context
	rt  Router
	t   *Table
	// orig holds saved rules as loaded, for the ones touched; nfPaths are 404 log rows a new exact redirect answers
	orig    map[*Rule]Rule
	created []*Rule
	deleted []*Rule
	nfPaths []string
	origin  string
	user    *string
}

func (b *batch) touch(r *Rule) {
	if _, ok := b.orig[r]; !ok && b.t.saved(r) {
		b.orig[r] = *r
	}
}

func (b *batch) create(r *Rule) {
	b.t.add(r)
	b.created = append(b.created, r)
}

func (b *batch) del(r *Rule) {
	b.t.remove(r)
	if b.t.saved(r) {
		b.touch(r)
		b.deleted = append(b.deleted, r)
		return
	}
	b.created = slices.DeleteFunc(b.created, func(x *Rule) bool { return x == r })
}

func (b *batch) set(r *Rule, change func(*Rule)) {
	b.touch(r)
	b.t.update(r, change)
}

func assign(from *Rule) func(*Rule) {
	return func(x *Rule) {
		x.Source, x.Match, x.Target, x.Status, x.Note, x.re = from.Source, from.Match, from.Target, from.Status, from.Note, from.re
	}
}

// put stores r as a new rule or over cur, checks it, and flattens rules that now chain through it. On a failed
// check the table is left as it was.
func (b *batch) put(cur, r *Rule, flatten bool) (*Rule, []string, []*Rule, error) {
	var undo func()
	if cur != nil {
		before := *cur
		_, touched := b.orig[cur]
		b.set(cur, assign(r))
		undo = func() {
			b.t.update(cur, assign(&before))
			if !touched {
				delete(b.orig, cur)
			}
		}
		r = cur
	} else {
		b.create(r)
		undo = func() { b.del(r) }
	}
	warns, err := check(b.ctx, b.rt, b.t, r)
	var ce *ChainError
	if errors.As(err, &ce) && ce.Code == "chain" && flatten {
		b.t.update(r, func(x *Rule) { x.Target = ce.Flattened })
		warns, err = check(b.ctx, b.rt, b.t, r)
	}
	if err != nil {
		undo()
		return nil, nil, nil, err
	}
	flat, err := b.flattenIncoming(r)
	if err != nil {
		return nil, nil, nil, err
	}
	return r, warns, flat, nil
}

// flattenIncoming points rules that would now chain through r at where r leads.
func (b *batch) flattenIncoming(r *Rule) ([]*Rule, error) {
	var out []*Rule
	for _, x := range b.t.pointingAt(r) {
		if x.Gone() {
			continue
		}
		tr, err := Resolve(b.ctx, b.rt, b.t, x.Target)
		if err != nil {
			return nil, err
		}
		if !tr.through(r) || tr.Final == "" || tr.End == EndLoop || tr.End == EndTooLong || tr.End == EndGone {
			continue
		}
		if p, _, ok := internal(tr.Final, b.rt.BaseURL()); ok && x.Match == "exact" && b.rt.Canonical(p) == x.Source {
			continue
		}
		b.set(x, func(x *Rule) { x.Target = tr.Final })
		out = append(out, x)
	}
	return out, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

const (
	sqlInsert = `
INSERT INTO redirects (source, match, target, status, note, origin, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7::uuid) RETURNING id`
	sqlUpdate = `
UPDATE redirects SET source = $2, match = $3, target = $4, status = $5, note = $6, updated_at = now() WHERE id = $1`
)

func (b *batch) persist(tx pgx.Tx) error {
	if len(b.deleted) > 0 {
		ids := make([]int64, len(b.deleted))
		for i, r := range b.deleted {
			ids[i] = r.ID
		}
		if _, err := tx.Exec(b.ctx, `DELETE FROM redirects WHERE id = ANY($1::bigint[])`, ids); err != nil {
			return err
		}
	}
	var updated []*Rule
	for r, o := range b.orig {
		if !slices.Contains(b.deleted, r) && (o.Source != r.Source || o.Match != r.Match || o.Target != r.Target ||
			o.Status != r.Status || o.Note != r.Note) {
			updated = append(updated, r)
		}
	}
	slices.SortFunc(updated, func(a, c *Rule) int { return cmpID(a.ID, c.ID) })
	for _, r := range updated {
		if _, err := tx.Exec(b.ctx, sqlUpdate, r.ID, r.Source, r.Match, nullable(r.Target), r.Status, r.Note); err != nil {
			return uniqueErr(err)
		}
	}
	for _, r := range b.created {
		var id int64
		if err := tx.QueryRow(b.ctx, sqlInsert, r.Source, r.Match, nullable(r.Target), r.Status, r.Note, b.origin, b.user).Scan(&id); err != nil {
			return uniqueErr(err)
		}
		b.t.setID(r, id)
	}
	if len(b.nfPaths) > 0 {
		if _, err := tx.Exec(b.ctx, `DELETE FROM not_found_log WHERE path = ANY($1::text[])`, b.nfPaths); err != nil {
			return err
		}
	}
	return nil
}

func uniqueErr(err error) error {
	if strings.Contains(err.Error(), "redirects_exact_source_idx") {
		return &FieldError{Field: "source", Message: "another redirect already has this exact source", Conflict: true}
	}
	return err
}

func (b *batch) purge() Purge {
	var p Purge
	seen := map[string]bool{}
	note := func(r Rule) {
		if r.Match != "exact" {
			p.Refresh = true
		} else if !seen[r.Source] {
			seen[r.Source] = true
			p.Paths = append(p.Paths, r.Source)
		}
	}
	for r, o := range b.orig {
		note(o)
		note(*r)
	}
	for _, r := range b.created {
		note(*r)
	}
	slices.Sort(p.Paths)
	return p
}

// withBatch loads the table under the redirect lock, runs fn, and writes its changes. fn returning an error rolls
// everything back.
func (s *Store) withBatch(ctx context.Context, origin string, user *string, fn func(*batch) error) (*batch, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(lockKey)); err != nil {
		return nil, err
	}
	rules, err := loadRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	b := &batch{ctx: ctx, rt: s.Router, t: NewTable(s.Router, rules), orig: map[*Rule]Rule{}, origin: origin, user: user}
	if err := fn(b); err != nil {
		return nil, err
	}
	if err := b.persist(tx); err != nil {
		return nil, err
	}
	return b, tx.Commit(ctx)
}

// Saved is the outcome of a save.
type Saved struct {
	ID        int64
	Warnings  []string
	Flattened []int64
	Purge     Purge
}

func ids(rules []*Rule) []int64 {
	out := make([]int64, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.ID)
	}
	slices.Sort(out)
	return out
}

// Save creates (id 0) or replaces a redirect. A chain is a *ChainError unless flatten is set, in which case the
// target becomes where the chain ends. Redirects that would chain through this one are flattened too.
func (s *Store) Save(ctx context.Context, id int64, in Input, flatten bool, user *string) (*Saved, error) {
	var rule *Rule
	var warns []string
	var flat []*Rule
	b, err := s.withBatch(ctx, "manual", user, func(b *batch) error {
		r, err := Normalize(in, s.Router)
		if err != nil {
			return err
		}
		var cur *Rule
		if id != 0 {
			if cur = b.t.byID[id]; cur == nil {
				return ErrNotFound
			}
		}
		if r.Match == "exact" {
			if other := b.t.exact[r.Source]; other != nil && other != cur {
				return &FieldError{Field: "source", Conflict: true,
					Message: fmt.Sprintf("redirect %d already has this exact source", other.ID)}
			}
			if cur == nil {
				b.nfPaths = []string{strings.TrimSpace(in.Source), r.Source}
			}
		}
		rule, warns, flat, err = b.put(cur, r, flatten)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Saved{ID: rule.ID, Warnings: warns, Flattened: ids(flat), Purge: b.purge()}, nil
}

// Delete removes a redirect.
func (s *Store) Delete(ctx context.Context, id int64) (Purge, error) {
	b, err := s.withBatch(ctx, "manual", nil, func(b *batch) error {
		r := b.t.byID[id]
		if r == nil {
			return ErrNotFound
		}
		b.del(r)
		return nil
	})
	if err != nil {
		return Purge{}, err
	}
	return b.purge(), nil
}

// ImportReport is the outcome of a CSV import. With any Errors nothing was written.
type ImportReport struct {
	Created  int         `json:"created"`
	Updated  int         `json:"updated"`
	Errors   []LineError `json:"errors"`
	Warnings []LineError `json:"warnings"`
	Purge    Purge       `json:"-"`
}

var errRejected = errors.New("import rejected")

// Import applies a CSV file all or nothing. Rows replace the redirect with the same exact source (or the same match
// and source for prefix and regex) and are checked in file order, so later rows see earlier ones.
func (s *Store) Import(ctx context.Context, lines []Line, flatten bool, user *string) (*ImportReport, error) {
	rep := &ImportReport{Errors: []LineError{}, Warnings: []LineError{}}
	b, err := s.withBatch(ctx, "manual", user, func(b *batch) error {
		firstLine := map[string]int{}
		for _, l := range lines {
			fail := func(field, msg string) {
				rep.Errors = append(rep.Errors, LineError{Line: l.N, Field: field, Message: msg})
			}
			r, err := Normalize(l.Input, s.Router)
			var fe *FieldError
			if errors.As(err, &fe) {
				fail(fe.Field, fe.Message)
				continue
			}
			key := r.Match + " " + r.Source
			if n, dup := firstLine[key]; dup {
				fail("source", fmt.Sprintf("same source as line %d", n))
				continue
			}
			firstLine[key] = l.N
			cur := b.t.find(r.Match, r.Source)
			_, warns, _, err := b.put(cur, r, flatten)
			var ce *ChainError
			switch {
			case errors.As(err, &fe):
				fail(fe.Field, fe.Message)
				continue
			case errors.As(err, &ce):
				fail("target", ce.Error())
				continue
			case err != nil:
				return err
			}
			if cur == nil {
				rep.Created++
				if r.Match == "exact" {
					b.nfPaths = append(b.nfPaths, l.Input.Source, r.Source)
				}
			} else {
				rep.Updated++
			}
			for _, w := range warns {
				rep.Warnings = append(rep.Warnings, LineError{Line: l.N, Message: w})
			}
		}
		if len(rep.Errors) > 0 {
			return errRejected
		}
		return nil
	})
	if errors.Is(err, errRejected) {
		rep.Created, rep.Updated = 0, 0
		return rep, nil
	}
	if err != nil {
		return nil, err
	}
	rep.Purge = b.purge()
	return rep, nil
}

// NotFound is one not_found_log row.
type NotFound struct {
	Path         string    `json:"path"`
	Hits         int64     `json:"hits"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	LastReferrer string    `json:"last_referrer"`
}

const MaxDismiss = 1000

// ListNotFound pages the 404 log, most hits first, or most recent first with sort=last_seen.
func (s *Store) ListNotFound(ctx context.Context, q, sort string, page, size int) ([]NotFound, int64, error) {
	order := "hits DESC, path"
	if sort == "last_seen" {
		order = "last_seen DESC, path"
	}
	where := `($1 = '' OR path ILIKE $2)`
	var total int64
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM not_found_log WHERE `+where, q, likePattern(q)).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT path, hits, first_seen, last_seen, last_referrer FROM not_found_log WHERE `+where+
		` ORDER BY `+order+` LIMIT $3 OFFSET $4`, q, likePattern(q), size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[NotFound])
	return out, total, err
}

// Dismiss deletes 404 log rows. A path that is hit again comes back with a fresh count.
func (s *Store) Dismiss(ctx context.Context, paths []string) (int64, error) {
	if len(paths) > MaxDismiss {
		return 0, fieldErr("paths", "at most %d paths", MaxDismiss)
	}
	tag, err := s.DB.Exec(ctx, `DELETE FROM not_found_log WHERE path = ANY($1::text[])`, paths)
	return tag.RowsAffected(), err
}
