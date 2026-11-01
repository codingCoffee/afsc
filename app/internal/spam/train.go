package spam

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Label is the training label of a comment status: approved is ham, spam is spam, anything else is unlabelled.
func Label(status string) string {
	switch status {
	case "approved":
		return "ham"
	case "spam":
		return "spam"
	}
	return ""
}

// Change is one comment moving between statuses. Tokens come from Tokens(body, author_url, author_email).
type Change struct {
	Tokens   []string
	From, To string
}

// Train applies label changes to spam_tokens incrementally inside tx, so the counts always match what Retrain
// would rebuild. Changes that keep the label are ignored.
func Train(ctx context.Context, tx pgx.Tx, changes []Change) error {
	type delta struct{ spam, ham int }
	d := map[string]*delta{}
	bump := func(label string, n int, tokens []string) {
		for _, t := range append([]string{MessagesToken}, tokens...) {
			x := d[t]
			if x == nil {
				x = &delta{}
				d[t] = x
			}
			if label == "spam" {
				x.spam += n
			} else {
				x.ham += n
			}
		}
	}
	for _, c := range changes {
		from, to := Label(c.From), Label(c.To)
		if from == to {
			continue
		}
		if from != "" {
			bump(from, -1, c.Tokens)
		}
		if to != "" {
			bump(to, 1, c.Tokens)
		}
	}
	tokens := make([]string, 0, len(d))
	for t, x := range d {
		if x.spam != 0 || x.ham != 0 {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	slices.Sort(tokens)
	spam, ham := make([]int32, len(tokens)), make([]int32, len(tokens))
	for i, t := range tokens {
		spam[i], ham[i] = int32(d[t].spam), int32(d[t].ham)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO spam_tokens (token) SELECT t FROM unnest($1::text[]) t ON CONFLICT DO NOTHING`,
		tokens); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
UPDATE spam_tokens st SET spam = greatest(st.spam + d.s, 0), ham = greatest(st.ham + d.h, 0)
FROM unnest($1::text[], $2::int[], $3::int[]) AS d(t, s, h) WHERE st.token = d.t`, tokens, spam, ham)
	return err
}

// Retrain rebuilds spam_tokens from every approved and spam comment plus the spam archive (the spam.retrain job).
// It locks spam_tokens first, so moderation waits instead of losing increments made during the rebuild.
func Retrain(ctx context.Context, pool *pgxpool.Pool) (Counts, error) {
	var msgs Counts
	tx, err := pool.Begin(ctx)
	if err != nil {
		return msgs, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE spam_tokens IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return msgs, err
	}
	counts := map[string]*Counts{}
	add := func(label string, tokens []string) {
		for _, t := range tokens {
			c := counts[t]
			if c == nil {
				c = &Counts{}
				counts[t] = c
			}
			if label == "spam" {
				c.Spam++
			} else {
				c.Ham++
			}
		}
		if label == "spam" {
			msgs.Spam++
		} else {
			msgs.Ham++
		}
	}
	rows, err := tx.Query(ctx, `SELECT status, body, author_url, author_email FROM comments WHERE status IN ('approved', 'spam')`)
	if err != nil {
		return msgs, err
	}
	var status, body, authorURL, email string
	if _, err := pgx.ForEachRow(rows, []any{&status, &body, &authorURL, &email}, func() error {
		add(Label(status), Tokens(body, authorURL, email))
		return nil
	}); err != nil {
		return msgs, err
	}
	rows, err = tx.Query(ctx, `SELECT tokens FROM spam_archive`)
	if err != nil {
		return msgs, err
	}
	var archived []string
	if _, err := pgx.ForEachRow(rows, []any{&archived}, func() error {
		add("spam", archived)
		return nil
	}); err != nil {
		return msgs, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM spam_tokens`); err != nil {
		return msgs, err
	}
	out := make([][]any, 0, len(counts)+1)
	out = append(out, []any{MessagesToken, msgs.Spam, msgs.Ham})
	for t, c := range counts {
		out = append(out, []any{t, c.Spam, c.Ham})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"spam_tokens"}, []string{"token", "spam", "ham"},
		pgx.CopyFromRows(out)); err != nil {
		return msgs, err
	}
	return msgs, tx.Commit(ctx)
}

// Stats returns the message totals the classifier was trained on.
func Stats(ctx context.Context, db Querier) (Counts, error) {
	var c Counts
	err := db.QueryRow(ctx, `SELECT coalesce(sum(spam), 0), coalesce(sum(ham), 0) FROM spam_tokens WHERE token = $1`,
		MessagesToken).Scan(&c.Spam, &c.Ham)
	return c, err
}
