package spam

import (
	"context"
	"math"
	"slices"

	"github.com/jackc/pgx/v5"
)

// MessagesToken is the spam_tokens row holding the spam and ham message totals. Real tokens never contain '#'.
const MessagesToken = "#messages"

// MinMessages of each label are needed before the classifier counts (§11).
const MinMessages = 200

const (
	interesting  = 15
	minSeen      = 5
	unknownProb  = 0.4
	minTokenProb = 0.01
	maxTokenProb = 0.99
)

type Counts struct{ Spam, Ham int }

// Corpus is the slice of spam_tokens a message needs: the totals and the counts of its own tokens.
type Corpus struct {
	Messages Counts
	Tokens   map[string]Counts
}

func (c *Corpus) Active() bool {
	return c.Messages.Spam >= MinMessages && c.Messages.Ham >= MinMessages
}

// tokenProb is Graham's per-token spam probability, with ham counts doubled to bias against false positives.
func (c *Corpus) tokenProb(t string) float64 {
	n := c.Tokens[t]
	good, bad := float64(2*n.Ham), float64(n.Spam)
	if good+bad < minSeen {
		return unknownProb
	}
	b := math.Min(1, bad/float64(max(c.Messages.Spam, 1)))
	g := math.Min(1, good/float64(max(c.Messages.Ham, 1)))
	if b+g == 0 {
		return unknownProb
	}
	return math.Max(minTokenProb, math.Min(maxTokenProb, b/(b+g)))
}

// Probability combines the 15 most interesting tokens (furthest from 0.5) into one spam probability.
func (c *Corpus) Probability(tokens []string) float64 {
	ps := make([]float64, 0, len(tokens))
	for _, t := range tokens {
		ps = append(ps, c.tokenProb(t))
	}
	slices.SortStableFunc(ps, func(a, b float64) int {
		da, db := math.Abs(a-0.5), math.Abs(b-0.5)
		switch {
		case da > db:
			return -1
		case da < db:
			return 1
		}
		return 0
	})
	if len(ps) > interesting {
		ps = ps[:interesting]
	}
	if len(ps) == 0 {
		return 0.5
	}
	var ls, lh float64
	for _, p := range ps {
		ls += math.Log(p)
		lh += math.Log(1 - p)
	}
	// p = Πp / (Πp + Π(1−p)), computed in log space
	return 1 / (1 + math.Exp(lh-ls))
}

// ClassifierScore maps a spam probability to a score delta (§11).
func ClassifierScore(p float64) int {
	switch {
	case p >= 0.99:
		return 6
	case p >= 0.9:
		return 3
	case p <= 0.1:
		return -3
	}
	return 0
}

// LoadCorpus reads the totals and the counts of tokens from spam_tokens.
func LoadCorpus(ctx context.Context, db Querier, tokens []string) (*Corpus, error) {
	c := &Corpus{Tokens: map[string]Counts{}}
	rows, err := db.Query(ctx, `SELECT token, spam, ham FROM spam_tokens WHERE token = ANY($1)`,
		append([]string{MessagesToken}, tokens...))
	if err != nil {
		return nil, err
	}
	var t string
	var n Counts
	_, err = pgx.ForEachRow(rows, []any{&t, &n.Spam, &n.Ham}, func() error {
		if t == MessagesToken {
			c.Messages = n
		} else {
			c.Tokens[t] = n
		}
		return nil
	})
	return c, err
}
