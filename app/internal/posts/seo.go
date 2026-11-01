package posts

import (
	"context"
	"errors"
	"net/http"

	"afsc/internal/seo"
)

// SeoFields is the seo table row; internal/seo owns its validation and storage.
type SeoFields = seo.Fields

func seoEmpty(f *SeoFields) bool {
	return f.Title == nil && f.Description == nil && f.Canonical == nil && f.RobotsNoindex == nil &&
		f.RobotsNofollow == nil && f.RobotsAdvanced == nil && f.OGTitle == nil && f.OGDescription == nil &&
		f.OGImageMediaID == nil && f.TwitterTitle == nil && f.TwitterDescription == nil &&
		f.TwitterImageMediaID == nil && f.FocusKeyphrase == nil && f.BreadcrumbTitle == nil && !f.IsCornerstone
}

// LoadSEO reads the seo row of an entity ("post", "term", "author" or "home"); a missing row is all defaults.
func LoadSEO(ctx context.Context, db seo.DB, entityType string, id int64) (SeoFields, error) {
	f, err := seo.LoadFields(ctx, db, entityType, id)
	if err != nil {
		return SeoFields{}, err
	}
	return *f, nil
}

// SaveSEO validates and writes an entity's seo row inside the caller's tx. Nil leaves it alone; all defaults
// deletes the row.
func SaveSEO(ctx context.Context, db seo.DB, entityType string, id int64, f *SeoFields) error {
	if f == nil {
		return nil
	}
	if err := f.Normalize(); err != nil {
		var fe *seo.FieldError
		if errors.As(err, &fe) {
			return fail(http.StatusBadRequest, "invalid_seo", "seo."+fe.Field, "%s", fe.Message)
		}
		return err
	}
	if seoEmpty(f) {
		_, err := db.Exec(ctx, `DELETE FROM seo WHERE entity_type = $1 AND entity_id = $2`, entityType, id)
		return err
	}
	return seo.SaveFields(ctx, db, entityType, id, f)
}
