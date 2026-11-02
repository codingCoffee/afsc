package spam

import (
	"context"
	"testing"
	"time"

	"afsc/internal/plugins/plugintest"
	"afsc/internal/settings"
	pipeline "afsc/internal/spam"
)

func TestSpamPluginIsTheCommentCheck(t *testing.T) {
	s := settings.Defaults()
	s.Plugins.Enabled = map[string]bool{"spam": false}
	rt := plugintest.Runtime(t, &s, New())
	now := time.Now()

	v := rt.CheckComment(context.Background(), pipeline.Input{Honeypot: "x", Issued: now.Add(-time.Minute), Now: now})
	if v.Hard != "spam" || len(v.Reasons) != 1 || v.Reasons[0].Check != "honeypot" {
		t.Errorf("always-on spam check didn't run: %+v", v)
	}
	// with no database the scored checks fail, and the comment is held
	v = rt.CheckComment(context.Background(), pipeline.Input{Body: "hello", Issued: now.Add(-time.Minute), Now: now})
	if !v.ForcePending || v.Reasons[len(v.Reasons)-1].Check != "check_failed" {
		t.Errorf("not fail-closed: %+v", v)
	}
}
