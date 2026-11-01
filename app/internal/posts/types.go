// Package posts is the content write service (§5, §7.2): saves, autosaves, revisions, scheduling and trash.
package posts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"afsc/internal/authz"
	"afsc/internal/content"
)

type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Actor is who makes a change. The scheduler acts with a zero User and role "".
type Actor struct {
	User uuid.UUID
	Role string
	IP   string
}

func (a Actor) can(c authz.Cap) bool { return authz.Can(a.Role, c) }

func (a Actor) ref() uuid.NullUUID { return uuid.NullUUID{UUID: a.User, Valid: a.User != uuid.Nil} }

func (a Actor) userRef() *string {
	if a.User == uuid.Nil {
		return nil
	}
	s := a.User.String()
	return &s
}

// Error is a refusal the API returns as JSON with Status.
type Error struct {
	Status         int             `json:"-"`
	Message        string          `json:"message"`
	Code           string          `json:"code,omitempty"`
	Field          string          `json:"field,omitempty"`
	Errors         []content.Issue `json:"errors,omitempty"`
	CurrentVersion *int            `json:"current_version,omitempty"`
	ModifiedBy     *string         `json:"modified_by,omitempty"`
	ModifiedAt     *time.Time      `json:"modified_at,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func fail(status int, code, field, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

func invalid(field, format string, args ...any) *Error {
	return fail(http.StatusUnprocessableEntity, "invalid", field, format, args...)
}

func notFound() *Error { return fail(http.StatusNotFound, "not_found", "", "post not found") }

func denied(format string, args ...any) *Error {
	return fail(http.StatusForbidden, "ownership", "", format, args...)
}

func noCap(format string, args ...any) *Error {
	return fail(http.StatusForbidden, "capability", "", format, args...)
}

var statuses = []string{"draft", "pending", "scheduled", "published", "private"}

type AuthorRef struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Slug        string `json:"slug"`
}

type TermRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Taxonomy string `json:"taxonomy"`
}

// Input is PostInput in client/app/types/api.ts.
type Input struct {
	Type              string          `json:"type"`
	Version           *int            `json:"version,omitempty"`
	Title             string          `json:"title"`
	Slug              string          `json:"slug"`
	Status            string          `json:"status"`
	PublishedAt       *time.Time      `json:"published_at"`
	Excerpt           string          `json:"excerpt"`
	Content           json.RawMessage `json:"content"`
	ParentID          *int64          `json:"parent_id"`
	Template          *string         `json:"template"`
	FeaturedMediaID   *int64          `json:"featured_media_id"`
	AuthorIDs         []int64         `json:"author_ids"`
	CategoryIDs       []int64         `json:"category_ids"`
	PrimaryCategoryID *int64          `json:"primary_category_id"`
	TagIDs            []int64         `json:"tag_ids"`
	CommentStatus     string          `json:"comment_status"`
	Sticky            bool            `json:"sticky"`
	MenuOrder         int             `json:"menu_order"`
	SEO               *SeoFields      `json:"seo,omitempty"`
}

type Autosave struct {
	RevisionID int64           `json:"revision_id"`
	Title      string          `json:"title"`
	Excerpt    string          `json:"excerpt"`
	Content    json.RawMessage `json:"content"`
	SEO        *SeoFields      `json:"seo"`
	SavedAt    time.Time       `json:"saved_at"`
}

// Detail is PostDetail.
type Detail struct {
	ID                int64           `json:"id"`
	Type              string          `json:"type"`
	Status            string          `json:"status"`
	Title             string          `json:"title"`
	Slug              string          `json:"slug"`
	PublishedAt       *time.Time      `json:"published_at"`
	Excerpt           string          `json:"excerpt"`
	Content           json.RawMessage `json:"content"`
	ParentID          *int64          `json:"parent_id"`
	Template          *string         `json:"template"`
	FeaturedMediaID   *int64          `json:"featured_media_id"`
	AuthorIDs         []int64         `json:"author_ids"`
	CategoryIDs       []int64         `json:"category_ids"`
	PrimaryCategoryID *int64          `json:"primary_category_id"`
	TagIDs            []int64         `json:"tag_ids"`
	CommentStatus     string          `json:"comment_status"`
	Sticky            bool            `json:"sticky"`
	MenuOrder         int             `json:"menu_order"`
	SEO               SeoFields       `json:"seo"`
	Version           int             `json:"version"`
	URL               *string         `json:"url"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	Authors           []AuthorRef     `json:"authors"`
	FeaturedMedia     *MediaItem      `json:"featured_media"`
	Autosave          *Autosave       `json:"autosave"`
	CanPublish        bool            `json:"can_publish"`
	CanOverwrite      bool            `json:"can_overwrite"`
}

// ListItem is PostListItem.
type ListItem struct {
	ID          int64       `json:"id"`
	Type        string      `json:"type"`
	Status      string      `json:"status"`
	Title       string      `json:"title"`
	Slug        string      `json:"slug"`
	URL         *string     `json:"url"`
	PublishedAt *time.Time  `json:"published_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Authors     []AuthorRef `json:"authors"`
	Categories  []TermRef   `json:"categories"`
	Tags        []TermRef   `json:"tags"`
	Sticky      bool        `json:"sticky"`
	Version     int         `json:"version"`
	CanEdit     bool        `json:"can_edit"`
}

// MediaItem is the MediaItem shape, for featured_media.
type MediaItem struct {
	ID              int64     `json:"id"`
	Filename        string    `json:"filename"`
	Mime            string    `json:"mime"`
	Bytes           int64     `json:"bytes"`
	Width           *int      `json:"width"`
	Height          *int      `json:"height"`
	Title           string    `json:"title"`
	Alt             string    `json:"alt"`
	Caption         string    `json:"caption"`
	Description     string    `json:"description"`
	FocalX          *float32  `json:"focal_x"`
	FocalY          *float32  `json:"focal_y"`
	URL             string    `json:"url"`
	ThumbURL        *string   `json:"thumb_url"`
	LibraryVisible  bool      `json:"library_visible"`
	LegacyPathCount int       `json:"legacy_path_count"`
	UploadedBy      *string   `json:"uploaded_by"`
	CreatedAt       time.Time `json:"created_at"`
}
