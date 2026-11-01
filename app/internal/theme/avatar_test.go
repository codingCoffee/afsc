package theme

import (
	"strings"
	"testing"
)

func TestAvatarFunc(t *testing.T) {
	dir := writeTheme(t, map[string]string{
		"theme.json":            `{"name":"t","image_sizes":{"thumb":{"width":150,"height":150,"crop":true}}}`,
		"templates/single.html": `{{avatar (index .Post.Authors 0) "thumb"}}|{{avatar (index .Post.Authors 1) "thumb"}}|{{avatar nil "thumb"}}`,
	})
	th, err := Load(dir, Options{Backend: FixtureBackend(&Manifest{})})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if _, err := th.Page(&b, Fixture(KindSingle, true)); err != nil {
		t.Fatal(err)
	}
	want := `<img src="/m/7/ab12cd34/c150x150/chart.png" alt="Author A" width="150" height="150" class="avatar" loading="lazy">|` +
		`<svg class="avatar avatar-initials" width="150" height="150" viewBox="0 0 150 150" role="img" aria-label="Guest Author B">` +
		`<rect width="100%" height="100%" fill="hsl(68 45% 40%)"/><text x="50%" y="50%" dy=".35em" text-anchor="middle" fill="#fff" ` +
		`font-family="sans-serif" font-size="60">GB</text></svg>|`
	if b.String() != want {
		t.Errorf("\n got %s\nwant %s", b.String(), want)
	}

	dir = writeTheme(t, map[string]string{"templates/single.html": `{{avatar (index .Post.Authors 0) "huge"}}`})
	th, err = Load(dir, Options{Backend: FixtureBackend(&Manifest{})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := th.Page(&b, Fixture(KindSingle, true)); err == nil || !strings.Contains(err.Error(), "huge") {
		t.Errorf("undeclared size: %v", err)
	}
}

func TestCommentAvatarIsInitials(t *testing.T) {
	dir := writeTheme(t, map[string]string{
		"theme.json":            `{"name":"t","image_sizes":{"thumb":{"width":40,"height":40,"crop":true}}}`,
		"templates/single.html": `{{avatar .Comments.Notice "thumb"}}`,
	})
	th, err := Load(dir, Options{Backend: FixtureBackend(&Manifest{})})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if _, err := th.Page(&b, Fixture(KindSingle, true)); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != string(InitialsAvatar("New Visitor", 40)) || !strings.Contains(got, ">NV</text>") {
		t.Errorf("comment avatar: %s", got)
	}
}
