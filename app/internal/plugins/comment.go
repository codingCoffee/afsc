package plugins

import (
	"context"

	"afsc/internal/spam"
)

// CheckComment runs the comment.check chain within its 2 s budget. It stops at a hard verdict, and fails closed:
// an error, panic or timeout makes the comment pending, keeping the reasons gathered so far.
func (rt *Runtime) CheckComment(ctx context.Context, in spam.Input) spam.Verdict {
	var v spam.Verdict
	if rt == nil {
		return v
	}
	ctx, cancel := context.WithTimeout(ctx, CommentCheck.Timeout)
	defer cancel()
	for _, hd := range rt.filters[CommentCheck.Name] {
		if !rt.Enabled(hd.plugin) {
			continue
		}
		fn := hd.fn.(func(context.Context, spam.Verdict, spam.Input) (spam.Verdict, error))
		prev := cloneVerdict(v)
		out, err := call(ctx, CommentCheck.Timeout, func(ctx context.Context) (spam.Verdict, error) { return fn(ctx, prev, in) })
		if err != nil {
			rt.logFailure(hd.plugin, CommentCheck.Name, err)
			v.ForcePending = true
			v.Reasons = append(v.Reasons, spam.Reason{Check: "check_failed", Detail: hd.plugin + ": " + err.Error()})
			return v
		}
		if v = out; v.Hard != "" {
			return v
		}
	}
	return v
}
