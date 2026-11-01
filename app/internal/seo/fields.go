package seo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Fields is one seo table row as the admin edits it (SeoFields in the admin API). nil means "not set".
type Fields struct {
	Title               *string  `json:"title"`
	Description         *string  `json:"description"`
	Canonical           *string  `json:"canonical"`
	RobotsNoindex       *bool    `json:"robots_noindex"`
	RobotsNofollow      *bool    `json:"robots_nofollow"`
	RobotsAdvanced      []string `json:"robots_advanced"`
	OGTitle             *string  `json:"og_title"`
	OGDescription       *string  `json:"og_description"`
	OGImageMediaID      *int64   `json:"og_image_media_id"`
	TwitterTitle        *string  `json:"twitter_title"`
	TwitterDescription  *string  `json:"twitter_description"`
	TwitterImageMediaID *int64   `json:"twitter_image_media_id"`
	FocusKeyphrase      *string  `json:"focus_keyphrase"`
	BreadcrumbTitle     *string  `json:"breadcrumb_title"`
	IsCornerstone       bool     `json:"is_cornerstone"`
}

// FieldError is a validation failure on one SeoFields field.
type FieldError struct{ Field, Message string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

var advancedRe = regexp.MustCompile(`^[a-z-]{1,40}(:-?[0-9a-z]{1,20})?$`)

// Normalize trims every string, turns empty strings into nil and checks lengths, the canonical URL and robots values.
func (f *Fields) Normalize() error {
	texts := []struct {
		name string
		p    **string
		max  int
	}{
		{"title", &f.Title, 300}, {"description", &f.Description, 1000}, {"canonical", &f.Canonical, 2048},
		{"og_title", &f.OGTitle, 300}, {"og_description", &f.OGDescription, 1000}, {"twitter_title", &f.TwitterTitle, 300},
		{"twitter_description", &f.TwitterDescription, 1000}, {"focus_keyphrase", &f.FocusKeyphrase, 200},
		{"breadcrumb_title", &f.BreadcrumbTitle, 200},
	}
	for _, t := range texts {
		if *t.p == nil {
			continue
		}
		s := strings.TrimSpace(**t.p)
		if s == "" {
			*t.p = nil
			continue
		}
		if utf8.RuneCountInString(s) > t.max {
			return &FieldError{t.name, fmt.Sprintf("must be at most %d characters", t.max)}
		}
		*t.p = &s
	}
	if c := f.Canonical; c != nil {
		u, err := url.Parse(*c)
		if OverrideCanonical("", *c) == "" || err != nil || (!strings.HasPrefix(*c, "/") && u.Host == "") {
			return &FieldError{"canonical", "must be a path starting with / or an http(s) URL"}
		}
	}
	if len(f.RobotsAdvanced) > 10 {
		return &FieldError{"robots_advanced", "at most 10 values"}
	}
	adv := f.RobotsAdvanced[:0]
	for _, a := range f.RobotsAdvanced {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if !advancedRe.MatchString(a) {
			return &FieldError{"robots_advanced", fmt.Sprintf("%q is not a robots directive", a)}
		}
		adv = append(adv, a)
	}
	f.RobotsAdvanced = adv
	if len(adv) == 0 {
		f.RobotsAdvanced = nil
	}
	for _, id := range []**int64{&f.OGImageMediaID, &f.TwitterImageMediaID} {
		if *id != nil && **id <= 0 {
			*id = nil
		}
	}
	return nil
}

// Override is the Build input for these fields. Images are left for the caller, which resolves media ids.
func (f *Fields) Override() *Override {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return &Override{Title: s(f.Title), Description: s(f.Description), Canonical: s(f.Canonical), NoIndex: f.RobotsNoindex,
		NoFollow: f.RobotsNofollow, Advanced: f.RobotsAdvanced, OGTitle: s(f.OGTitle), OGDescription: s(f.OGDescription),
		TwitterTitle: s(f.TwitterTitle), TwitterDescription: s(f.TwitterDescription)}
}

// DB is what LoadFields and SaveFields need: a pool or a transaction.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const (
	sqlLoadFields = `
SELECT title, description, canonical, robots_noindex, robots_nofollow, robots_advanced, og_title, og_description,
       og_image_media_id, twitter_title, twitter_description, twitter_image_media_id, focus_keyphrase, breadcrumb_title,
       is_cornerstone
FROM seo WHERE entity_type = $1 AND entity_id = $2`
	sqlSaveFields = `
INSERT INTO seo (entity_type, entity_id, title, description, canonical, robots_noindex, robots_nofollow, robots_advanced,
                 og_title, og_description, og_image_media_id, twitter_title, twitter_description, twitter_image_media_id,
                 focus_keyphrase, breadcrumb_title, is_cornerstone)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (entity_type, entity_id) DO UPDATE SET
  title = EXCLUDED.title, description = EXCLUDED.description, canonical = EXCLUDED.canonical,
  robots_noindex = EXCLUDED.robots_noindex, robots_nofollow = EXCLUDED.robots_nofollow,
  robots_advanced = EXCLUDED.robots_advanced, og_title = EXCLUDED.og_title, og_description = EXCLUDED.og_description,
  og_image_media_id = EXCLUDED.og_image_media_id, twitter_title = EXCLUDED.twitter_title,
  twitter_description = EXCLUDED.twitter_description, twitter_image_media_id = EXCLUDED.twitter_image_media_id,
  focus_keyphrase = EXCLUDED.focus_keyphrase, breadcrumb_title = EXCLUDED.breadcrumb_title,
  is_cornerstone = EXCLUDED.is_cornerstone, updated_at = now()`
)

// LoadFields reads an entity's seo row. An entity without one gets empty fields.
func LoadFields(ctx context.Context, db DB, entityType string, id int64) (*Fields, error) {
	f := &Fields{}
	err := db.QueryRow(ctx, sqlLoadFields, entityType, id).Scan(&f.Title, &f.Description, &f.Canonical, &f.RobotsNoindex,
		&f.RobotsNofollow, &f.RobotsAdvanced, &f.OGTitle, &f.OGDescription, &f.OGImageMediaID, &f.TwitterTitle,
		&f.TwitterDescription, &f.TwitterImageMediaID, &f.FocusKeyphrase, &f.BreadcrumbTitle, &f.IsCornerstone)
	if errors.Is(err, pgx.ErrNoRows) {
		return &Fields{}, nil
	}
	if len(f.RobotsAdvanced) == 0 {
		f.RobotsAdvanced = nil
	}
	return f, err
}

// SaveFields upserts an entity's seo row. Call Normalize first.
func SaveFields(ctx context.Context, db DB, entityType string, id int64, f *Fields) error {
	_, err := db.Exec(ctx, sqlSaveFields, entityType, id, f.Title, f.Description, f.Canonical, f.RobotsNoindex,
		f.RobotsNofollow, f.RobotsAdvanced, f.OGTitle, f.OGDescription, f.OGImageMediaID, f.TwitterTitle,
		f.TwitterDescription, f.TwitterImageMediaID, f.FocusKeyphrase, f.BreadcrumbTitle, f.IsCornerstone)
	return err
}
