// Package comments creates and moderates comments (§11). Spam scoring lives in internal/spam.
package comments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/plugins"
	"afsc/internal/settings"
	"afsc/internal/site"
	"afsc/internal/spam"
)

var (
	ErrBadToken    = errors.New("comment form token is invalid")
	ErrClosed      = errors.New("comments are closed")
	ErrRateLimited = errors.New("too many comments, try again later")
	ErrNotFound    = errors.New("comment or post not found")
)

const (
	rateWindow   = 10 * time.Minute
	ratePerIP    = 5
	ratePerEmail = 3
	checkTimeout = 2 * time.Second
)

// CheckFunc is the comment.check chain: the plugin runtime's, or plain spam.Check without one.
type CheckFunc func(ctx context.Context, db spam.Querier, in spam.Input) (spam.Verdict, error)

type Service struct {
	DB     *pgxpool.Pool
	Site   *site.Site
	Signer *spam.Signer
	Check  CheckFunc
	Now    func() time.Time
	Logf   func(format string, args ...any)
}

func New(pool *pgxpool.Pool, st *site.Site, signer *spam.Signer, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Service{DB: pool, Site: st, Signer: signer, Check: spam.Check, Now: time.Now, Logf: logf}
	if rt := st.Plugins(); rt.HasFilter(plugins.CommentCheck.Name) {
		s.Check = func(ctx context.Context, _ spam.Querier, in spam.Input) (spam.Verdict, error) {
			return rt.CheckComment(ctx, in), nil
		}
	}
	return s
}

// Input is a public comment submission.
type Input struct {
	PostID, ParentID       int64
	Token                  string
	Name, Email, URL, Body string
	Honeypot               string
	IP, UserAgent          string
}

// Result is a stored comment.
type Result struct {
	ID       int64
	PostID   int64
	ParentID int64
	Status   string
}

func (s *Service) discussion() settings.Discussion { return s.Site.Settings().Discussion }

// Create runs a public submission through validation, the rate limit and the spam pipeline, then stores it.
// Spam is stored too, so the caller answers it like any accepted comment.
func (s *Service) Create(ctx context.Context, in Input) (*Result, error) {
	now := s.Now()
	postID, issued, ok := s.Signer.ParseFormToken(in.Token)
	if !ok || postID != in.PostID {
		return nil, ErrBadToken
	}
	if verr := normalize(&in); verr != nil {
		return nil, verr
	}
	d := s.discussion()
	var commentStatus, status string
	var publishedAt *time.Time
	err := s.DB.QueryRow(ctx, `SELECT comment_status, status, published_at FROM posts WHERE id = $1 AND type IN ('post', 'page')`,
		in.PostID).Scan(&commentStatus, &status, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (status != "published" || publishedAt == nil || publishedAt.After(now))) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !d.OpenFor(commentStatus, publishedAt, now) {
		return nil, ErrClosed
	}
	parentID, err := s.resolveParent(ctx, s.DB, in.PostID, in.ParentID, d.ThreadDepth)
	if err != nil {
		return nil, err
	}

	var ip *netip.Addr
	if a, err := netip.ParseAddr(in.IP); err == nil {
		a = a.Unmap()
		ip = &a
	}
	if err := s.rateLimit(ctx, ip, in.Email, now); err != nil {
		return nil, err
	}

	sin := spam.Input{Name: in.Name, Email: in.Email, URL: in.URL, Body: in.Body, Honeypot: in.Honeypot,
		Issued: issued, Now: now}
	if ip != nil {
		sin.IP = *ip
	}
	v := s.check(ctx, sin)
	trusted := false
	if v.Hard == "" && !v.ForcePending && v.Score < d.ModerationThreshold && d.TrustedAutoApprove {
		if trusted, err = spam.Trusted(ctx, s.DB, in.Email); err != nil {
			s.Logf("comments: trusted lookup: %v", err)
			trusted = false
		}
	}
	newStatus := spam.Decide(v, d, trusted)
	reasons, _ := json.Marshal(v.Reasons)

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	res := &Result{PostID: in.PostID, ParentID: parentID, Status: newStatus}
	var ua *string
	if in.UserAgent != "" {
		u := truncate(in.UserAgent, 500)
		ua = &u
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO comments (post_id, parent_id, author_name, author_email, author_url, ip, user_agent, body, status,
                      spam_score, spam_reasons)
VALUES ($1, nullif($2::bigint, 0), $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		in.PostID, parentID, in.Name, in.Email, in.URL, ip, ua, in.Body, newStatus, float32(v.Score), reasons,
	).Scan(&res.ID); err != nil {
		return nil, err
	}
	if err := spam.Train(ctx, tx, []spam.Change{{Tokens: spam.Tokens(in.Body, in.URL, in.Email), To: newStatus}}); err != nil {
		return nil, err
	}
	if newStatus == "approved" {
		if err := s.purge(ctx, tx, []int64{in.PostID}); err != nil {
			return nil, err
		}
	}
	if newStatus != "spam" {
		if err := s.emitCreated(ctx, tx, res, in.Name); err != nil {
			return nil, err
		}
	}
	return res, tx.Commit(ctx)
}

// check runs the comment.check filter and fails closed: an error, panic or timeout makes the comment pending.
func (s *Service) check(ctx context.Context, in spam.Input) (v spam.Verdict) {
	failed := func(detail string) spam.Verdict {
		return spam.Verdict{ForcePending: true, Reasons: []spam.Reason{{Check: "check_failed", Detail: detail}}}
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	type out struct {
		v   spam.Verdict
		err error
	}
	ch := make(chan out, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- out{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		v, err := s.Check(cctx, s.DB, in)
		ch <- out{v, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			s.Logf("comments: spam check failed: %v", o.err)
			return failed(o.err.Error())
		}
		return o.v
	case <-cctx.Done():
		s.Logf("comments: spam check timed out")
		return failed("timeout")
	}
}

func (s *Service) emitCreated(ctx context.Context, tx pgx.Tx, res *Result, author string) error {
	rt := s.Site.Plugins()
	if rt == nil {
		return nil
	}
	urls, err := s.Site.VisibleURLs(ctx, []int64{res.PostID})
	if err != nil {
		return err
	}
	d := plugins.CommentData{ID: res.ID, PostID: res.PostID, PostURL: urls[res.PostID], Status: res.Status, AuthorName: author}
	if res.ParentID != 0 {
		d.ParentID = &res.ParentID
	}
	return plugins.Emit(ctx, rt, tx, plugins.CommentCreated, d)
}

func (s *Service) emitStatus(ctx context.Context, tx pgx.Tx, changed []Changed) error {
	for _, c := range changed {
		if err := plugins.Emit(ctx, s.Site.Plugins(), tx, plugins.CommentStatusChanged,
			plugins.CommentStatusData{ID: c.ID, PostID: c.PostID, From: c.From, To: c.To}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) rateLimit(ctx context.Context, ip *netip.Addr, email string, now time.Time) error {
	since := now.Add(-rateWindow)
	var byIP, byEmail int
	if err := s.DB.QueryRow(ctx, `
SELECT (SELECT count(*) FROM comments WHERE $1::inet IS NOT NULL AND ip = $1 AND created_at > $3),
       (SELECT count(*) FROM comments WHERE author_email = $2 AND created_at > $3)`,
		ip, email, since).Scan(&byIP, &byEmail); err != nil {
		return err
	}
	if byIP >= ratePerIP || byEmail >= ratePerEmail {
		return ErrRateLimited
	}
	return nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// resolveParent checks the parent is an approved comment on the same post and reparents a reply that would
// exceed depth onto the deepest allowed ancestor (Q8). It returns 0 for a top-level comment.
func (s *Service) resolveParent(ctx context.Context, db querier, postID, parentID int64, depth int) (int64, error) {
	if parentID <= 0 {
		return 0, nil
	}
	rows, err := db.Query(ctx, `
WITH RECURSIVE chain AS (
  SELECT id, parent_id, post_id, status, 1 AS n FROM comments WHERE id = $1
  UNION ALL
  SELECT c.id, c.parent_id, c.post_id, c.status, chain.n + 1 FROM comments c JOIN chain ON c.id = chain.parent_id
  WHERE chain.n < 100
)
SELECT id, post_id, status FROM chain ORDER BY n`, parentID)
	if err != nil {
		return 0, err
	}
	type link struct {
		id, postID int64
		status     string
	}
	var chain []link
	var l link
	if _, err := pgx.ForEachRow(rows, []any{&l.id, &l.postID, &l.status}, func() error {
		chain = append(chain, l)
		return nil
	}); err != nil {
		return 0, err
	}
	if len(chain) == 0 || chain[0].postID != postID || chain[0].status != "approved" {
		return 0, &ValidationError{Fields: map[string]string{"parent_id": "The comment you replied to is not available."}}
	}
	// chain[0] is the parent at depth len(chain); the reply would sit one deeper
	if len(chain)+1 <= depth {
		return chain[0].id, nil
	}
	if depth <= 1 {
		return 0, nil
	}
	return chain[len(chain)-depth+1].id, nil
}

func (s *Service) purge(ctx context.Context, tx pgx.Tx, postIDs []int64) error {
	ev, err := s.Site.PostPathEvent(ctx, postIDs)
	if err != nil {
		return err
	}
	return site.Notify(ctx, tx, ev)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}
