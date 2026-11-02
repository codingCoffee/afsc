package decisions

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFailsClosed(t *testing.T) {
	f := &File{Items: []*Item{New("shortcode", "caption", 3, nil, "convert", "")}}
	if err := f.Check(); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("want unconfirmed error, got %v", err)
	}
	f.Items[0].Status = StatusConfirmed
	if err := f.Check(); err != nil {
		t.Fatal(err)
	}
	f.Items[0].Decision = "explode"
	if err := f.Check(); err == nil || !strings.Contains(err.Error(), "not in") {
		t.Fatalf("want invalid decision, got %v", err)
	}
}

func TestNarrowedAllowed(t *testing.T) {
	it := New("shortcode", "myplugin", 1, nil, "render_php", "", "render_php", "drop", "keep_text")
	it.Decision, it.Status = "convert", StatusConfirmed
	if err := (&File{Items: []*Item{it}}).Check(); err == nil {
		t.Fatal("convert is not allowed without a handler")
	}
}

func TestRequiredOptions(t *testing.T) {
	it := New("permalink_structure", "/%author%/%postname%/", 1, nil, "change", "")
	it.Status = StatusConfirmed
	f := &File{Items: []*Item{it}}
	if err := f.Check(); err == nil || !strings.Contains(err.Error(), "options.pattern") {
		t.Fatalf("want missing option, got %v", err)
	}
	it.Options = map[string]any{"pattern": "/{slug}/"}
	if err := f.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestMergeKeepsConfirmed(t *testing.T) {
	old := &File{Items: []*Item{
		{ID: "shortcode:gallery", Kind: "shortcode", Name: "gallery", Decision: "drop", Status: StatusConfirmed},
		{ID: "shortcode:gone", Kind: "shortcode", Name: "gone", Decision: "drop", Status: StatusConfirmed},
		{ID: "block:core/html", Kind: "block", Name: "core/html", Decision: "raw_html", Status: StatusProposed},
	}}
	fresh := &File{Items: []*Item{
		New("shortcode", "gallery", 5, nil, "convert", ""),
		New("block", "core/html", 1, nil, "convert", ""),
		New("shortcode", "new", 1, nil, "keep_text", ""),
	}}
	m := Merge(old, fresh)
	if it := m.Get("shortcode", "gallery"); it.Decision != "drop" || it.Status != StatusConfirmed || it.Count != 5 {
		t.Errorf("gallery: %+v", it)
	}
	if it := m.Get("block", "core/html"); it.Decision != "convert" || it.Status != StatusProposed {
		t.Errorf("unconfirmed old decision should not carry: %+v", it)
	}
	if it := m.Get("shortcode", "gone"); it == nil || !it.Stale {
		t.Errorf("gone should be stale: %+v", it)
	}
	if got := m.Decision("shortcode", "gone", "default"); got != "default" {
		t.Errorf("stale decision used: %s", got)
	}

	p := filepath.Join(t.TempDir(), "decisions.json")
	if err := m.Save(p); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil || back.Get("shortcode", "new") == nil {
		t.Fatalf("round trip: %v", err)
	}
}
