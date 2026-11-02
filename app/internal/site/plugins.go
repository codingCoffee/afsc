package site

import (
	"context"

	"afsc/internal/theme"
)

// Posts, PurgePosts and ThemeManifest implement plugins.Backend.

// Posts returns the published posts and pages among ids in the given order, hydrated like listing cards.
func (st *Site) Posts(ctx context.Context, ids []int64) ([]*theme.Post, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := st.postsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := st.now()
	live := rows[:0]
	for _, p := range rows {
		if p.ref.Status == "published" && p.ref.PublishedAt != nil && !p.ref.PublishedAt.After(now) {
			live = append(live, p)
		}
	}
	snap := st.snap.Load()
	if err := st.hydrate(ctx, snap, live, hydrateOpts{}); err != nil {
		return nil, err
	}
	byID := make(map[int64]*theme.Post, len(live))
	for _, p := range live {
		byID[p.ref.ID] = p.view
	}
	out := make([]*theme.Post, 0, len(live))
	for _, id := range ids {
		if v := byID[id]; v != nil {
			out = append(out, v)
			delete(byID, id)
		}
	}
	return out, nil
}

// PurgePosts drops the cached single views of these posts here and on every replica (all pages past 200).
func (st *Site) PurgePosts(ctx context.Context, ids []int64) error {
	refs, err := st.postRefs(ctx, ids)
	if err != nil {
		return err
	}
	snap := st.snap.Load()
	set := pathSet{}
	for _, r := range refs {
		if r.Status == "published" {
			set.add(snap.refPath(r))
		}
	}
	return st.Invalidate(ctx, set.event(false))
}

// ThemeManifest is the active theme's theme.json.
func (st *Site) ThemeManifest() *theme.Manifest {
	if st.theme == nil {
		return nil
	}
	return st.theme.Manifest()
}
