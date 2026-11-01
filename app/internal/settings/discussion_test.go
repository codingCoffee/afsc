package settings

import (
	"strings"
	"testing"
	"time"
)

func TestDiscussionValidation(t *testing.T) {
	cases := map[string]func(*Discussion){
		"spam_threshold":       func(d *Discussion) { d.SpamThreshold = 0 },
		"moderation_threshold": func(d *Discussion) { d.ModerationThreshold = d.SpamThreshold },
		"comments_per_page":    func(d *Discussion) { d.CommentsPerPage = 501 },
	}
	for field, mod := range cases {
		a := Defaults()
		mod(&a.Discussion)
		if err := a.Validate(); err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: %v", field, err)
		}
	}
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	all, _, err := Decode([]byte(`{"discussion":{"comments_enabled":true,"close_after_days":0,"thread_depth":3}}`))
	if err != nil || all.Discussion.SpamThreshold != 6 || all.Discussion.CommentsPerPage != 50 || !all.Discussion.TrustedAutoApprove {
		t.Fatalf("older bundles get the new defaults: %+v %v", all.Discussion, err)
	}
}

func TestOpenFor(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -40)
	d := Defaults().Discussion
	if !d.OpenFor("open", &old, now) || d.OpenFor("closed", &old, now) {
		t.Fatal("comment_status")
	}
	d.CloseAfterDays = 30
	if d.OpenFor("open", &old, now) {
		t.Fatal("closed after 30 days")
	}
	recent := now.AddDate(0, 0, -5)
	if !d.OpenFor("open", &recent, now) {
		t.Fatal("recent post closed")
	}
	d.CommentsEnabled = false
	if d.OpenFor("open", &recent, now) {
		t.Fatal("global switch ignored")
	}
}
