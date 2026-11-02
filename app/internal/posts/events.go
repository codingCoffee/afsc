package posts

import (
	"context"

	"github.com/jackc/pgx/v5"

	"afsc/internal/plugins"
)

func (s *Service) postData(r *row) plugins.PostData {
	ref := r.ref()
	return plugins.PostData{ID: r.ID, Type: r.Type, Status: r.Status, Title: r.Title, Slug: r.Slug,
		URL: s.Site.PostURL(&ref), ModifiedAt: r.UpdatedAt}
}

// emitChange fires post.saved when saved, plus post.published or post.unpublished when the status crosses
// published. Trash counts as unpublishing.
func (s *Service) emitChange(ctx context.Context, tx pgx.Tx, cur, after *row, saved bool) error {
	rt := s.Site.Plugins()
	if rt == nil {
		return nil
	}
	if saved {
		if err := plugins.Emit(ctx, rt, tx, plugins.PostSaved, s.postData(after)); err != nil {
			return err
		}
	}
	was := cur != nil && cur.Status == "published"
	switch is := after.Status == "published"; {
	case is && !was:
		d := s.postData(after)
		d.PublishedAt = after.PublishedAt
		return plugins.Emit(ctx, rt, tx, plugins.PostPublished, d)
	case was && !is:
		d := s.postData(after)
		d.PreviousStatus = cur.Status
		return plugins.Emit(ctx, rt, tx, plugins.PostUnpublished, d)
	}
	return nil
}
