package plugins

import (
	"time"

	"github.com/google/uuid"
)

// Event names an action and the type of its data. Payloads never carry commenter emails, IPs, user agents,
// comment bodies or setting values.
type Event[D any] struct{ Name string }

// Envelope is what a subscriber receives. ID is stable across retries: handlers must be idempotent on it.
type Envelope[D any] struct {
	ID         uuid.UUID `json:"id"`
	Event      string    `json:"event"`
	OccurredAt time.Time `json:"occurred_at"`
	Data       D         `json:"data"`
}

type PostData struct {
	ID             int64      `json:"id"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	Title          string     `json:"title"`
	Slug           string     `json:"slug"`
	URL            string     `json:"url"`
	ModifiedAt     time.Time  `json:"modified_at"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	PreviousStatus string     `json:"previous_status,omitempty"`
}

type PostDeletedData struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
	Slug string `json:"slug"`
}

type TermData struct {
	ID       int64  `json:"id"`
	Taxonomy string `json:"taxonomy"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	URL      string `json:"url"`
}

type CommentData struct {
	ID         int64  `json:"id"`
	PostID     int64  `json:"post_id"`
	PostURL    string `json:"post_url"` // empty when the post isn't public
	ParentID   *int64 `json:"parent_id"`
	Status     string `json:"status"`
	AuthorName string `json:"author_name"`
}

type CommentStatusData struct {
	ID     int64  `json:"id"`
	PostID int64  `json:"post_id"`
	From   string `json:"from"`
	To     string `json:"to"` // pending, approved, spam, trash or deleted
}

type MediaData struct {
	ID       int64  `json:"id"`
	URL      string `json:"url"`
	Mime     string `json:"mime"`
	Width    *int   `json:"width"`
	Height   *int   `json:"height"`
	Filename string `json:"filename"`
}

type LoginData struct {
	UserID   uuid.UUID `json:"user_id"`
	Username string    `json:"username"`
	Role     string    `json:"role"`
}

type SettingsData struct {
	Group  string   `json:"group"`
	Fields []string `json:"fields"` // changed field names, never values
}

var (
	PostSaved            = Event[PostData]{"post.saved"}
	PostPublished        = Event[PostData]{"post.published"}
	PostUnpublished      = Event[PostData]{"post.unpublished"}
	PostDeleted          = Event[PostDeletedData]{"post.deleted"}
	TermSaved            = Event[TermData]{"term.saved"}
	CommentCreated       = Event[CommentData]{"comment.created"}
	CommentStatusChanged = Event[CommentStatusData]{"comment.status_changed"}
	MediaUploaded        = Event[MediaData]{"media.uploaded"}
	UserLogin            = Event[LoginData]{"user.login"}
	SettingsChanged      = Event[SettingsData]{"settings.changed"}
)

var EventNames = []string{PostSaved.Name, PostPublished.Name, PostUnpublished.Name, PostDeleted.Name, TermSaved.Name,
	CommentCreated.Name, CommentStatusChanged.Name, MediaUploaded.Name, UserLogin.Name, SettingsChanged.Name}
