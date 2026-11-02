// Package spam is the comment spam pipeline (§11): hard rules, scored checks, a Bayesian classifier, training and
// retention. Check is what the built-in spam plugin runs as its comment.check filter.
package spam

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/settings"
)

type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const (
	minFillTime    = 3 * time.Second
	maxTokenAge    = 24 * time.Hour
	duplicateSince = 30 * 24 * time.Hour
	freeLinks      = 2
)

// Input is a submitted comment as the pipeline sees it.
type Input struct {
	Name, Email, URL, Body string
	IP                     netip.Addr // zero when unknown
	Honeypot               string
	Issued                 time.Time // from the form token
	Now                    time.Time
}

// Reason is one check's contribution, stored in comments.spam_reasons.
type Reason struct {
	Check  string `json:"check"`
	Delta  int    `json:"delta"`
	Detail string `json:"detail,omitempty"`
}

// Verdict is the pipeline's result. Hard is "spam" when a hard rule fired; ForcePending overrides auto-approval.
type Verdict struct {
	Score        int
	Reasons      []Reason
	Hard         string
	ForcePending bool
}

func (v *Verdict) add(check string, delta int, detail string) {
	v.Score += delta
	v.Reasons = append(v.Reasons, Reason{Check: check, Delta: delta, Detail: detail})
}

// Merge adds o's score and reasons to v. A hard verdict or ForcePending in either one carries over.
func (v Verdict) Merge(o Verdict) Verdict {
	v.Score += o.Score
	v.Reasons = append(v.Reasons, o.Reasons...)
	v.ForcePending = v.ForcePending || o.ForcePending
	if v.Hard == "" {
		v.Hard = o.Hard
	}
	return v
}

// Check runs the pipeline. Hard rules short-circuit; the rest add score deltas.
func Check(ctx context.Context, db Querier, in Input) (Verdict, error) {
	var v Verdict
	if in.Honeypot != "" {
		v.Hard = "spam"
		v.Reasons = []Reason{{Check: "honeypot"}}
		return v, nil
	}
	age := in.Now.Sub(in.Issued)
	if age < minFillTime {
		v.Hard = "spam"
		v.Reasons = []Reason{{Check: "too_fast", Detail: age.Round(time.Millisecond).String()}}
		return v, nil
	}
	if age > maxTokenAge {
		v.ForcePending = true
		v.add("stale_token", 0, age.Round(time.Minute).String())
	}

	list, err := LoadBlocklist(ctx, db)
	if err != nil {
		return v, err
	}
	for _, r := range MatchBlocklist(list, in) {
		v.add(r.Check, r.Delta, r.Detail)
	}
	for _, r := range LinkReasons(in) {
		v.add(r.Check, r.Delta, r.Detail)
	}

	var dups int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM comments WHERE body_hash = afsc_body_hash($1) AND created_at > $2`,
		in.Body, in.Now.Add(-duplicateSince)).Scan(&dups); err != nil {
		return v, err
	}
	if dups > 0 {
		v.add("duplicate", 5, fmt.Sprintf("%d in 30 days", dups))
	}

	tokens := Tokens(in.Body, in.URL, in.Email)
	corpus, err := LoadCorpus(ctx, db, tokens)
	if err != nil {
		return v, err
	}
	if corpus.Active() {
		p := corpus.Probability(tokens)
		if d := ClassifierScore(p); d != 0 {
			v.add("classifier", d, fmt.Sprintf("p=%.3f", p))
		}
	}
	return v, nil
}

// LinkReasons scores links: +2 per body link beyond two, +5 for a link in the name.
func LinkReasons(in Input) []Reason {
	var out []Reason
	if n := len(Links(in.Body)); n > freeLinks {
		out = append(out, Reason{Check: "links", Delta: 2 * (n - freeLinks), Detail: fmt.Sprintf("%d links", n)})
	}
	if len(Links(in.Name)) > 0 {
		out = append(out, Reason{Check: "link_in_name", Delta: 5})
	}
	return out
}

// Decide maps a verdict to a status (§11). trusted means the email already has an approved comment.
func Decide(v Verdict, d settings.Discussion, trusted bool) string {
	switch {
	case v.Hard != "":
		return v.Hard
	case v.Score >= d.SpamThreshold:
		return "spam"
	case v.Score >= d.ModerationThreshold, v.ForcePending:
		return "pending"
	case d.TrustedAutoApprove && trusted:
		return "approved"
	}
	return "pending"
}

// Trusted reports whether email has at least one approved comment.
func Trusted(ctx context.Context, db Querier, email string) (bool, error) {
	if strings.TrimSpace(email) == "" {
		return false, nil
	}
	var ok bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM comments WHERE author_email = $1 AND status = 'approved')`,
		email).Scan(&ok)
	return ok, err
}
