package site

import "context"

// PostPathEvent purges the cached page of each visible post, for comment approvals. The post's group covers
// its ?cpage variants.
func (st *Site) PostPathEvent(ctx context.Context, postIDs []int64) (Event, error) {
	refs, err := st.postRefs(ctx, postIDs)
	if err != nil {
		return Event{}, err
	}
	s := st.snap.Load()
	set := pathSet{}
	now := st.now()
	for _, r := range refs {
		if r.Visible(now) {
			set.add(s.refPath(r))
		}
	}
	return set.event(false), nil
}

// CommentPostURLs maps each visible post to its absolute URL; hidden posts are left out.
func (st *Site) CommentPostURLs(ctx context.Context, postIDs []int64) (map[int64]string, error) {
	refs, err := st.postRefs(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	s := st.snap.Load()
	out := map[int64]string{}
	now := st.now()
	for id, r := range refs {
		if r.Visible(now) {
			out[id] = s.abs(s.refPath(r))
		}
	}
	return out, nil
}
