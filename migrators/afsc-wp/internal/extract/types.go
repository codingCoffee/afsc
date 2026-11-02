// Package extract holds the decoded WordPress data set (work/extract) shared by every later phase.
// Values are raw but decoded: charset-fixed, serialized options unserialized, slugs percent-decoded.
package extract

import (
	"sort"
	"strconv"
)

type Meta map[string][]string

// First returns the first value of key, "" when absent.
func (m Meta) First(key string) string {
	if v := m[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

type Post struct {
	ID            int64   `json:"id"`
	Author        int64   `json:"author"`
	Date          string  `json:"date"`
	DateGMT       string  `json:"date_gmt"`
	Modified      string  `json:"modified"`
	ModifiedGMT   string  `json:"modified_gmt"`
	Title         string  `json:"title"`
	Content       string  `json:"content"`
	Excerpt       string  `json:"excerpt"`
	Status        string  `json:"status"`
	CommentStatus string  `json:"comment_status"`
	Password      string  `json:"password"`
	Name          string  `json:"name"`
	Slug          string  `json:"slug"`
	Parent        int64   `json:"parent"`
	GUID          string  `json:"guid"`
	MenuOrder     int     `json:"menu_order"`
	Type          string  `json:"type"`
	MimeType      string  `json:"mime_type"`
	TermTaxIDs    []int64 `json:"term_tax_ids,omitempty"`
	Meta          Meta    `json:"meta,omitempty"`
}

type User struct {
	ID          int64  `json:"id"`
	Login       string `json:"login"`
	Nicename    string `json:"nicename"`
	Email       string `json:"email"`
	URL         string `json:"url"`
	Registered  string `json:"registered"`
	DisplayName string `json:"display_name"`
	Meta        Meta   `json:"meta,omitempty"`
}

type Term struct {
	TermTaxID   int64  `json:"term_tax_id"`
	TermID      int64  `json:"term_id"`
	Name        string `json:"name"`
	RawSlug     string `json:"raw_slug"`
	Slug        string `json:"slug"`
	Taxonomy    string `json:"taxonomy"`
	Description string `json:"description"`
	Parent      int64  `json:"parent"`
	Count       int64  `json:"count"`
	Meta        Meta   `json:"meta,omitempty"`
}

type Comment struct {
	ID          int64  `json:"id"`
	PostID      int64  `json:"post_id"`
	Author      string `json:"author"`
	AuthorEmail string `json:"author_email"`
	AuthorURL   string `json:"author_url"`
	AuthorIP    string `json:"author_ip"`
	Date        string `json:"date"`
	DateGMT     string `json:"date_gmt"`
	Content     string `json:"content"`
	Approved    string `json:"approved"`
	Agent       string `json:"agent"`
	Type        string `json:"type"`
	Parent      int64  `json:"parent"`
	UserID      int64  `json:"user_id"`
}

type Table struct {
	Name      string `json:"name"`
	Collation string `json:"collation"`
}

// Fix records one mojibake repair, by legacy key and field only.
type Fix struct {
	LegacyKey string `json:"legacy_key"`
	Field     string `json:"field"`
}

// File is one file under uploads/, relative to the uploads root with forward slashes.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Media  bool   `json:"media"`
	SHA256 string `json:"sha256,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Count struct {
	Key   string `json:"key"`
	Sub   string `json:"sub,omitempty"`
	Count int64  `json:"count"`
}

// Stats are aggregate counts the inventory needs but the extract doesn't keep rows for.
type Stats struct {
	PostTypeStatus  []Count  `json:"post_type_status"`
	PostMetaKeys    []Count  `json:"postmeta_keys"`
	UserMetaKeys    []Count  `json:"usermeta_keys"`
	TermMetaKeys    []Count  `json:"termmeta_keys"`
	Taxonomies      []Count  `json:"taxonomies"`
	CommentTypes    []Count  `json:"comment_types"`
	OptionNames     []string `json:"option_names"`
	SecretOptions   []string `json:"secret_options"`
	SkippedUserMeta []string `json:"skipped_usermeta_keys"`
	TotalUsers      int64    `json:"total_users"`
}

type Dataset struct {
	Posts    []*Post        `json:"-"`
	Users    []*User        `json:"-"`
	Terms    []*Term        `json:"-"`
	Comments []*Comment     `json:"-"`
	Options  map[string]any `json:"-"`
	Tables   []Table        `json:"-"`
	Fixes    []Fix          `json:"-"`
	Files    []*File        `json:"-"`
	Stats    Stats          `json:"-"`
	Prefix   string         `json:"-"`

	byID map[int64]*Post
}

// Post returns the post with id, nil when it isn't in the set.
func (d *Dataset) Post(id int64) *Post {
	if d.byID == nil {
		d.byID = make(map[int64]*Post, len(d.Posts))
		for _, p := range d.Posts {
			d.byID[p.ID] = p
		}
	}
	return d.byID[id]
}

// Option returns a decoded option value.
func (d *Dataset) Option(name string) any { return d.Options[name] }

// OptionString returns an option as a string ("" when absent or not scalar).
func (d *Dataset) OptionString(name string) string {
	switch v := d.Options[name].(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		if v {
			return "1"
		}
		return ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// OptionMap returns an option decoded to a map, nil otherwise.
func (d *Dataset) OptionMap(name string) map[string]any {
	m, _ := d.Options[name].(map[string]any)
	return m
}

// Sort puts every slice in a stable order so outputs are deterministic.
func (d *Dataset) Sort() {
	sort.Slice(d.Posts, func(i, j int) bool { return d.Posts[i].ID < d.Posts[j].ID })
	sort.Slice(d.Users, func(i, j int) bool { return d.Users[i].ID < d.Users[j].ID })
	sort.Slice(d.Terms, func(i, j int) bool { return d.Terms[i].TermTaxID < d.Terms[j].TermTaxID })
	sort.Slice(d.Comments, func(i, j int) bool { return d.Comments[i].ID < d.Comments[j].ID })
	sort.Slice(d.Files, func(i, j int) bool { return d.Files[i].Path < d.Files[j].Path })
	sort.Slice(d.Tables, func(i, j int) bool { return d.Tables[i].Name < d.Tables[j].Name })
	sort.Slice(d.Fixes, func(i, j int) bool {
		if d.Fixes[i].LegacyKey != d.Fixes[j].LegacyKey {
			return d.Fixes[i].LegacyKey < d.Fixes[j].LegacyKey
		}
		return d.Fixes[i].Field < d.Fixes[j].Field
	})
	d.byID = nil
}
