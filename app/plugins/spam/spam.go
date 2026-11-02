// Package spam is the built-in comment.check filter: the §11 pipeline (hard rules, scored checks, classifier) from
// internal/spam. It's always on; other plugins' checks run after it and add to its verdict.
package spam

import (
	"context"

	"afsc/internal/plugins"
	pipeline "afsc/internal/spam"
)

type Plugin struct{}

func New() Plugin { return Plugin{} }

func (Plugin) ID() string { return "spam" }

func (Plugin) Info() plugins.Info {
	return plugins.Info{Name: "Spam filter", AlwaysOn: true,
		Description: "Honeypot, timing, blocklist, link and duplicate checks plus the Bayesian classifier for comments."}
}

func (Plugin) Init(_ context.Context, h *plugins.Host) error {
	plugins.AddFilter(h, plugins.CommentCheck, 0, func(ctx context.Context, v pipeline.Verdict, in pipeline.Input) (pipeline.Verdict, error) {
		got, err := pipeline.Check(ctx, h.DB(), in)
		if err != nil {
			return v, err
		}
		return v.Merge(got), nil
	})
	return nil
}
