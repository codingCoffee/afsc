// Package importer implements `afsc import` and the bundle format shared with the migration project (§17).
package importer

import (
  "bufio"
  "bytes"
  "encoding/json"
  "errors"
  "fmt"
  "io"
  "io/fs"
  "os"
  "path/filepath"
  "time"
)

const FormatVersion = 1

type Manifest struct {
  FormatVersion int            `json:"format_version"`
  Source        string         `json:"source"`
  GeneratedAt   time.Time      `json:"generated_at"`
  Counts        map[string]int `json:"counts"`
  Theme         string         `json:"theme"`
}

type SEORec struct {
  Title               *string  `json:"title"`
  Description         *string  `json:"description"`
  Canonical           *string  `json:"canonical"`
  RobotsNoindex       *bool    `json:"robots_noindex"`
  RobotsNofollow      *bool    `json:"robots_nofollow"`
  RobotsAdvanced      []string `json:"robots_advanced"`
  OGTitle             *string  `json:"og_title"`
  OGDescription       *string  `json:"og_description"`
  OGImageMedia        *string  `json:"og_image_media"`
  TwitterTitle        *string  `json:"twitter_title"`
  TwitterDescription  *string  `json:"twitter_description"`
  TwitterImageMedia   *string  `json:"twitter_image_media"`
  FocusKeyphrase      *string  `json:"focus_keyphrase"`
  BreadcrumbTitle     *string  `json:"breadcrumb_title"`
  IsCornerstone       bool     `json:"is_cornerstone"`
}

type UserRec struct {
  LegacyKey   string `json:"legacy_key"`
  Email       string `json:"email"`
  DisplayName string `json:"display_name"`
  Role        string `json:"role"`
  Disabled    bool   `json:"disabled"`
}

type AuthorRec struct {
  LegacyKey   string            `json:"legacy_key"`
  Slug        string            `json:"slug"`
  DisplayName string            `json:"display_name"`
  Bio         string            `json:"bio"`
  Website     string            `json:"website"`
  Social      map[string]string `json:"social"`
  AvatarMedia *string           `json:"avatar_media"`
  User        *string           `json:"user"`
  SEO         *SEORec           `json:"seo"`
}

type LegacyPathRec struct {
  Path    string `json:"path"`
  Variant string `json:"variant"`
  Width   *int   `json:"width"`
  Height  *int   `json:"height"`
  Crop    bool   `json:"crop"`
}

type MediaRec struct {
  LegacyKey       string          `json:"legacy_key"`
  File            string          `json:"file"`
  SHA256          string          `json:"sha256"`
  Filename        string          `json:"filename"`
  Mime            string          `json:"mime"`
  Width           *int            `json:"width"`
  Height          *int            `json:"height"`
  DurationSeconds *int            `json:"duration_seconds"`
  Title           string          `json:"title"`
  Alt             string          `json:"alt"`
  Caption         string          `json:"caption"`
  Description     string          `json:"description"`
  LibraryVisible  *bool           `json:"library_visible"`
  CreatedAt       *time.Time      `json:"created_at"`
  LegacyPaths     []LegacyPathRec `json:"legacy_paths"`
}

type TermRec struct {
  LegacyKey   string  `json:"legacy_key"`
  Taxonomy    string  `json:"taxonomy"`
  Slug        string  `json:"slug"`
  Name        string  `json:"name"`
  Description string  `json:"description"`
  Parent      *string `json:"parent"`
  SEO         *SEORec `json:"seo"`
}

type PostTermRec struct {
  Term    string `json:"term"`
  Primary bool   `json:"primary"`
}

type VideoRec struct {
  Provider        string     `json:"provider"`
  ExternalID      string     `json:"external_id"`
  URL             string     `json:"url"`
  Title           string     `json:"title"`
  Description     string     `json:"description"`
  ThumbnailURL    string     `json:"thumbnail_url"`
  ThumbnailMedia  *string    `json:"thumbnail_media"`
  DurationSeconds *int       `json:"duration_seconds"`
  UploadDate      *time.Time `json:"upload_date"`
  FamilyFriendly  *bool      `json:"family_friendly"`
}

type PostRec struct {
  LegacyKey     string          `json:"legacy_key"`
  Type          string          `json:"type"`
  Status        string          `json:"status"`
  Slug          string          `json:"slug"`
  Parent        *string         `json:"parent"`
  Title         string          `json:"title"`
  Excerpt       string          `json:"excerpt"`
  Content       json.RawMessage `json:"content"`
  CreatedAt     *time.Time      `json:"created_at"`
  UpdatedAt     *time.Time      `json:"updated_at"`
  PublishedAt   *time.Time      `json:"published_at"`
  Authors       []string        `json:"authors"`
  Terms         []PostTermRec   `json:"terms"`
  FeaturedMedia *string         `json:"featured_media"`
  Template      *string         `json:"template"`
  Sticky        bool            `json:"sticky"`
  MenuOrder     int             `json:"menu_order"`
  CommentStatus string          `json:"comment_status"`
  FeedGUID      *string         `json:"feed_guid"`
  SEO           *SEORec         `json:"seo"`
  Videos        []VideoRec      `json:"videos"`
}

type CommentRec struct {
  LegacyKey   string     `json:"legacy_key"`
  Post        string     `json:"post"`
  Parent      *string    `json:"parent"`
  AuthorName  string     `json:"author_name"`
  AuthorEmail string     `json:"author_email"`
  AuthorURL   string     `json:"author_url"`
  User        *string    `json:"user"`
  IP          string     `json:"ip"`
  UserAgent   string     `json:"user_agent"`
  Body        string     `json:"body"`
  Status      string     `json:"status"`
  CreatedAt   *time.Time `json:"created_at"`
}

type MenuItemRec struct {
  LegacyKey  string   `json:"legacy_key"`
  Parent     *string  `json:"parent"`
  Position   int      `json:"position"`
  Label      string   `json:"label"`
  Kind       string   `json:"kind"`
  Target     *string  `json:"target"`
  URL        *string  `json:"url"`
  NewTab     bool     `json:"new_tab"`
  CSSClasses []string `json:"css_classes"`
}

type MenuRec struct {
  LegacyKey string        `json:"legacy_key"`
  Slug      string        `json:"slug"`
  Name      string        `json:"name"`
  Location  *string       `json:"location"`
  Items     []MenuItemRec `json:"items"`
}

type RedirectRec struct {
  LegacyKey string  `json:"legacy_key"`
  Source    string  `json:"source"`
  Match     string  `json:"match"`
  Target    *string `json:"target"`
  Status    int     `json:"status"`
  Note      string  `json:"note"`
}

type LegacyRec struct {
  LegacyKey string          `json:"legacy_key"`
  Kind      string          `json:"kind"`
  Source    json.RawMessage `json:"source"`
}

// Bundle file names, in import order.
const (
  fileUsers     = "users.ndjson"
  fileMedia     = "media.ndjson"
  fileAuthors   = "authors.ndjson"
  fileTerms     = "terms.ndjson"
  filePosts     = "posts.ndjson"
  fileComments  = "comments.ndjson"
  fileMenus     = "menus.ndjson"
  fileRedirects = "redirects.ndjson"
  fileLegacy    = "legacy.ndjson"
)

func readManifest(dir string) (*Manifest, error) {
  b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
  if err != nil {
    return nil, fmt.Errorf("manifest.json: %w", err)
  }
  var m Manifest
  if err := strictUnmarshal(b, &m); err != nil {
    return nil, fmt.Errorf("manifest.json: %w", err)
  }
  return &m, nil
}

func strictUnmarshal(b []byte, v any) error {
  dec := json.NewDecoder(bytes.NewReader(b))
  dec.DisallowUnknownFields()
  if err := dec.Decode(v); err != nil {
    return err
  }
  if dec.More() {
    return errors.New("trailing data after json value")
  }
  return nil
}

// recordReader pulls records from an ndjson file. A missing file reads as empty.
type recordReader struct {
  name string
  f    *os.File
  r    *bufio.Reader
  line int
}

func openRecords(dir, name string) (*recordReader, error) {
  f, err := os.Open(filepath.Join(dir, name))
  if errors.Is(err, fs.ErrNotExist) {
    return &recordReader{name: name}, nil
  }
  if err != nil {
    return nil, err
  }
  return &recordReader{name: name, f: f, r: bufio.NewReaderSize(f, 1<<20)}, nil
}

func (rr *recordReader) Close() {
  if rr.f != nil {
    rr.f.Close()
  }
}

// next decodes the next non-blank line into v. It returns false at the end of the file.
func (rr *recordReader) next(v any) (bool, error) {
  if rr.r == nil {
    return false, nil
  }
  for {
    b, err := rr.r.ReadBytes('\n')
    rr.line++
    if len(bytes.TrimSpace(b)) > 0 {
      if uerr := strictUnmarshal(b, v); uerr != nil {
        return false, fmt.Errorf("%s:%d: %w", rr.name, rr.line, uerr)
      }
      return true, nil
    }
    if errors.Is(err, io.EOF) {
      return false, nil
    }
    if err != nil {
      return false, fmt.Errorf("%s:%d: %w", rr.name, rr.line, err)
    }
  }
}

// eachRecord streams an ndjson file, calling fn with the 1-based line number of each record.
func eachRecord[T any](dir, name string, fn func(line int, rec *T) error) error {
  rr, err := openRecords(dir, name)
  if err != nil {
    return err
  }
  defer rr.Close()
  for {
    var rec T
    ok, err := rr.next(&rec)
    if err != nil || !ok {
      return err
    }
    if err := fn(rr.line, &rec); err != nil {
      return err
    }
  }
}
