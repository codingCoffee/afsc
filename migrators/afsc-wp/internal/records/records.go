// Package records mirrors the bundle record shapes in AFSC's docs/import-format.md. A test strict-decodes every
// shape into afsc/internal/importer's types, so drift fails the build.
package records

import (
	"encoding/json"
	"time"
)

type Manifest struct {
	FormatVersion int            `json:"format_version"`
	Source        string         `json:"source"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Counts        map[string]int `json:"counts"`
	Theme         string         `json:"theme,omitempty"`
}

type SEO struct {
	Title              *string  `json:"title,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Canonical          *string  `json:"canonical,omitempty"`
	RobotsNoindex      *bool    `json:"robots_noindex,omitempty"`
	RobotsNofollow     *bool    `json:"robots_nofollow,omitempty"`
	RobotsAdvanced     []string `json:"robots_advanced,omitempty"`
	OGTitle            *string  `json:"og_title,omitempty"`
	OGDescription      *string  `json:"og_description,omitempty"`
	OGImageMedia       *string  `json:"og_image_media,omitempty"`
	TwitterTitle       *string  `json:"twitter_title,omitempty"`
	TwitterDescription *string  `json:"twitter_description,omitempty"`
	TwitterImageMedia  *string  `json:"twitter_image_media,omitempty"`
	FocusKeyphrase     *string  `json:"focus_keyphrase,omitempty"`
	BreadcrumbTitle    *string  `json:"breadcrumb_title,omitempty"`
	IsCornerstone      bool     `json:"is_cornerstone,omitempty"`
}

// Empty reports whether the object carries nothing, so it can be omitted.
func (s *SEO) Empty() bool {
	return s == nil || (s.Title == nil && s.Description == nil && s.Canonical == nil && s.RobotsNoindex == nil &&
		s.RobotsNofollow == nil && len(s.RobotsAdvanced) == 0 && s.OGTitle == nil && s.OGDescription == nil &&
		s.OGImageMedia == nil && s.TwitterTitle == nil && s.TwitterDescription == nil && s.TwitterImageMedia == nil &&
		s.FocusKeyphrase == nil && s.BreadcrumbTitle == nil && !s.IsCornerstone)
}

type User struct {
	LegacyKey   string `json:"legacy_key"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled,omitempty"`
}

type Author struct {
	LegacyKey   string            `json:"legacy_key"`
	Slug        string            `json:"slug"`
	DisplayName string            `json:"display_name"`
	Bio         string            `json:"bio,omitempty"`
	Website     string            `json:"website,omitempty"`
	Social      map[string]string `json:"social,omitempty"`
	AvatarMedia *string           `json:"avatar_media,omitempty"`
	User        *string           `json:"user,omitempty"`
	SEO         *SEO              `json:"seo,omitempty"`
}

type LegacyPath struct {
	Path    string `json:"path"`
	Variant string `json:"variant"`
	Width   *int   `json:"width,omitempty"`
	Height  *int   `json:"height,omitempty"`
	Crop    bool   `json:"crop,omitempty"`
}

type Media struct {
	LegacyKey      string       `json:"legacy_key"`
	File           string       `json:"file"`
	SHA256         string       `json:"sha256"`
	Filename       string       `json:"filename"`
	Mime           string       `json:"mime"`
	Width          *int         `json:"width,omitempty"`
	Height         *int         `json:"height,omitempty"`
	Title          string       `json:"title,omitempty"`
	Alt            string       `json:"alt,omitempty"`
	Caption        string       `json:"caption,omitempty"`
	Description    string       `json:"description,omitempty"`
	LibraryVisible *bool        `json:"library_visible,omitempty"`
	CreatedAt      *time.Time   `json:"created_at,omitempty"`
	LegacyPaths    []LegacyPath `json:"legacy_paths,omitempty"`

	// SourcePath is the uploads-relative file to copy into files/; never written to the bundle.
	SourcePath string `json:"-"`
}

type Term struct {
	LegacyKey   string  `json:"legacy_key"`
	Taxonomy    string  `json:"taxonomy"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Parent      *string `json:"parent,omitempty"`
	SEO         *SEO    `json:"seo,omitempty"`
}

type PostTerm struct {
	Term    string `json:"term"`
	Primary bool   `json:"primary,omitempty"`
}

type Video struct {
	Provider        string     `json:"provider,omitempty"`
	ExternalID      string     `json:"external_id,omitempty"`
	URL             string     `json:"url"`
	Title           string     `json:"title,omitempty"`
	Description     string     `json:"description,omitempty"`
	ThumbnailURL    string     `json:"thumbnail_url,omitempty"`
	ThumbnailMedia  *string    `json:"thumbnail_media,omitempty"`
	DurationSeconds *int       `json:"duration_seconds,omitempty"`
	UploadDate      *time.Time `json:"upload_date,omitempty"`
	FamilyFriendly  *bool      `json:"family_friendly,omitempty"`
}

type Post struct {
	LegacyKey     string          `json:"legacy_key"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	Slug          string          `json:"slug"`
	Parent        *string         `json:"parent,omitempty"`
	Title         string          `json:"title"`
	Excerpt       string          `json:"excerpt,omitempty"`
	Content       json.RawMessage `json:"content"`
	CreatedAt     *time.Time      `json:"created_at"`
	UpdatedAt     *time.Time      `json:"updated_at,omitempty"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
	Authors       []string        `json:"authors,omitempty"`
	Terms         []PostTerm      `json:"terms,omitempty"`
	FeaturedMedia *string         `json:"featured_media,omitempty"`
	Template      *string         `json:"template,omitempty"`
	Sticky        bool            `json:"sticky,omitempty"`
	MenuOrder     int             `json:"menu_order,omitempty"`
	CommentStatus string          `json:"comment_status,omitempty"`
	FeedGUID      *string         `json:"feed_guid,omitempty"`
	SEO           *SEO            `json:"seo,omitempty"`
	Videos        []Video         `json:"videos,omitempty"`
}

type Comment struct {
	LegacyKey   string     `json:"legacy_key"`
	Post        string     `json:"post"`
	Parent      *string    `json:"parent,omitempty"`
	AuthorName  string     `json:"author_name,omitempty"`
	AuthorEmail string     `json:"author_email,omitempty"`
	AuthorURL   string     `json:"author_url,omitempty"`
	User        *string    `json:"user,omitempty"`
	IP          string     `json:"ip,omitempty"`
	UserAgent   string     `json:"user_agent,omitempty"`
	Body        string     `json:"body"`
	Status      string     `json:"status"`
	CreatedAt   *time.Time `json:"created_at"`
}

type MenuItem struct {
	LegacyKey  string   `json:"legacy_key"`
	Parent     *string  `json:"parent,omitempty"`
	Position   int      `json:"position"`
	Label      string   `json:"label"`
	Kind       string   `json:"kind"`
	Target     *string  `json:"target,omitempty"`
	URL        *string  `json:"url,omitempty"`
	NewTab     bool     `json:"new_tab"`
	CSSClasses []string `json:"css_classes"`
}

type Menu struct {
	LegacyKey string     `json:"legacy_key"`
	Slug      string     `json:"slug"`
	Name      string     `json:"name"`
	Location  *string    `json:"location,omitempty"`
	Items     []MenuItem `json:"items"`
}

type Redirect struct {
	LegacyKey string  `json:"legacy_key"`
	Source    string  `json:"source"`
	Match     string  `json:"match"`
	Target    *string `json:"target,omitempty"`
	Status    int     `json:"status"`
	Note      string  `json:"note,omitempty"`
}

type Legacy struct {
	LegacyKey string         `json:"legacy_key"`
	Kind      string         `json:"kind"`
	Source    map[string]any `json:"source"`
}

func Ptr[T any](v T) *T { return &v }
