package records

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"afsc/internal/importer"
)

// strict decodes a record the way the importer does (unknown fields are errors).
func strict(t *testing.T, v any, into any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%T does not match the importer: %v\n%s", v, err, b)
	}
}

func TestShapesMatchImporter(t *testing.T) {
	now := time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC)
	seo := &SEO{Title: Ptr("t"), Description: Ptr("d"), Canonical: Ptr("/x/"), RobotsNoindex: Ptr(true),
		RobotsNofollow: Ptr(false), RobotsAdvanced: []string{"noarchive"}, OGTitle: Ptr("o"), OGDescription: Ptr("o"),
		OGImageMedia: Ptr("wp:post:1"), TwitterTitle: Ptr("t"), TwitterDescription: Ptr("t"), TwitterImageMedia: Ptr("wp:post:1"),
		FocusKeyphrase: Ptr("k"), BreadcrumbTitle: Ptr("b"), IsCornerstone: true}
	strict(t, Manifest{FormatVersion: 1, Source: "wp", GeneratedAt: now, Counts: map[string]int{"posts": 1}, Theme: "x"}, &importer.Manifest{})
	strict(t, User{LegacyKey: "wp:user:1", Email: "user@example.com", DisplayName: "Author A", Role: "author", Disabled: true}, &importer.UserRec{})
	strict(t, Author{LegacyKey: "wp:user:1", Slug: "a", DisplayName: "A", Bio: "b", Website: "https://example.com",
		Social: map[string]string{"x": "https://x.com/a"}, AvatarMedia: Ptr("wp:post:1"), User: Ptr("wp:user:1"), SEO: seo}, &importer.AuthorRec{})
	strict(t, Media{LegacyKey: "wp:post:1", File: "files/ab/ab.png", SHA256: "ab", Filename: "a.png", Mime: "image/png",
		Width: Ptr(1), Height: Ptr(1), Title: "t", Alt: "a", Caption: "c", Description: "d", LibraryVisible: Ptr(false),
		CreatedAt: &now, LegacyPaths: []LegacyPath{{Path: "/wp-content/uploads/a.png", Variant: "resize", Width: Ptr(1), Height: Ptr(1), Crop: true}}}, &importer.MediaRec{})
	strict(t, Term{LegacyKey: "wp:term:1", Taxonomy: "category", Slug: "s", Name: "n", Description: "d", Parent: Ptr("wp:term:2"), SEO: seo}, &importer.TermRec{})
	strict(t, Post{LegacyKey: "wp:post:2", Type: "post", Status: "published", Slug: "s", Parent: Ptr("wp:post:3"), Title: "t",
		Excerpt: "e", Content: json.RawMessage(`{"type":"doc"}`), CreatedAt: &now, UpdatedAt: &now, PublishedAt: &now,
		Authors: []string{"wp:user:1"}, Terms: []PostTerm{{Term: "wp:term:1", Primary: true}}, FeaturedMedia: Ptr("wp:post:1"),
		Template: Ptr("wide"), Sticky: true, MenuOrder: 2, CommentStatus: "open", FeedGUID: Ptr("g"), SEO: seo,
		Videos: []Video{{Provider: "youtube", ExternalID: "x", URL: "https://youtu.be/x", Title: "t", Description: "d",
			ThumbnailURL: "https://example.com/t.jpg", ThumbnailMedia: Ptr("wp:post:1"), DurationSeconds: Ptr(3), UploadDate: &now, FamilyFriendly: Ptr(true)}}}, &importer.PostRec{})
	strict(t, Comment{LegacyKey: "wp:comment:1", Post: "wp:post:2", Parent: Ptr("wp:comment:0"), AuthorName: "n",
		AuthorEmail: "user@example.com", AuthorURL: "https://example.com", User: Ptr("wp:user:1"), IP: "192.0.2.1",
		UserAgent: "ua", Body: "b", Status: "approved", CreatedAt: &now}, &importer.CommentRec{})
	strict(t, Menu{LegacyKey: "wp:menu:1", Slug: "m", Name: "M", Location: Ptr("primary"), Items: []MenuItem{{LegacyKey: "wp:menu_item:2",
		Parent: Ptr("wp:menu_item:1"), Position: 1, Label: "l", Kind: "url", Target: Ptr("wp:post:1"), URL: Ptr("/"), NewTab: true,
		CSSClasses: []string{"c"}}}}, &importer.MenuRec{})
	strict(t, Redirect{LegacyKey: "wp:redirect:1", Source: "/a/", Match: "exact", Target: Ptr("/b/"), Status: 301, Note: "n"}, &importer.RedirectRec{})
	strict(t, Legacy{LegacyKey: "wp:post:2", Kind: "post", Source: map[string]any{"post_content": "x"}}, &importer.LegacyRec{})
}
