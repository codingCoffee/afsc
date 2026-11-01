package site

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/sanitize"
	"afsc/internal/theme"
)

const (
	// CommentsPath is the only public write endpoint (§11).
	CommentsPath = "/comments"
	// NoticeCookie holds the visitor's just-posted comment id; its afsc_ prefix makes the page cache step aside.
	NoticeCookie = "afsc_comment"
	// HoneypotField is the hidden form field bots fill in.
	HoneypotField = "website_url"
)

var wpCommentKeyRe = regexp.MustCompile(`:comment:([0-9]{1,18})$`)

// commentAnchor keeps imported comments on their WordPress id, so old #comment-N links still land (Q7).
func commentAnchor(id int64, legacyKey string) string {
	if m := wpCommentKeyRe.FindStringSubmatch(legacyKey); m != nil {
		return "comment-" + m[1]
	}
	return "comment-" + strconv.FormatInt(id, 10)
}

type commentRow struct {
	id, parentID int64
	name, url    string
	body         string
	created      time.Time
	legacyKey    string
}

func (r *commentRow) view(depth int) *theme.Comment {
	return &theme.Comment{ID: r.id, Anchor: commentAnchor(r.id, r.legacyKey), AuthorName: r.name, AuthorURL: r.url,
		BodyHTML: template.HTML(sanitize.Comment(r.body)), Date: r.created, Depth: depth}
}

// thread is a post's approved comments arranged for display.
type thread struct {
	roots []*theme.Comment // top-level comments, oldest first
	byID  map[int64]*theme.Comment
	root  map[int64]int // comment id → index of its top-level comment in roots
}

// loadThread reads a post's approved comments into a tree. Replies deeper than depth are listed after their
// parent at the deepest level, and replies whose parent isn't approved become top-level.
func (st *Site) loadThread(ctx context.Context, postID int64, depth int) (*thread, error) {
	rows, err := st.db.Query(ctx, `
SELECT id, coalesce(parent_id, 0), author_name, author_url, body, created_at, coalesce(legacy_key, '')
FROM comments WHERE post_id = $1 AND status = 'approved' ORDER BY created_at, id`, postID)
	if err != nil {
		return nil, err
	}
	var all []*commentRow
	byID := map[int64]*commentRow{}
	var r commentRow
	if _, err := pgx.ForEachRow(rows, []any{&r.id, &r.parentID, &r.name, &r.url, &r.body, &r.created, &r.legacyKey}, func() error {
		c := r
		all = append(all, &c)
		byID[c.id] = &c
		return nil
	}); err != nil {
		return nil, err
	}
	kids := map[int64][]*commentRow{}
	var tops []*commentRow
	for _, c := range all {
		if c.parentID != 0 && byID[c.parentID] != nil && c.parentID != c.id {
			kids[c.parentID] = append(kids[c.parentID], c)
		} else {
			tops = append(tops, c)
		}
	}
	t := &thread{byID: map[int64]*theme.Comment{}, root: map[int64]int{}}
	seen := map[int64]bool{}
	var place func(c *commentRow, into *[]*theme.Comment, d, rootIdx int)
	place = func(c *commentRow, into *[]*theme.Comment, d, rootIdx int) {
		if seen[c.id] {
			return
		}
		seen[c.id] = true
		v := c.view(d)
		*into = append(*into, v)
		t.byID[c.id], t.root[c.id] = v, rootIdx
		for _, k := range kids[c.id] {
			if d < depth {
				place(k, &v.Children, d+1, rootIdx)
			} else {
				place(k, into, d, rootIdx)
			}
		}
	}
	for i, c := range tops {
		place(c, &t.roots, 1, i)
	}
	return t, nil
}

func setReplyURLs(cs []*theme.Comment, postURL string, depth int) {
	for _, c := range cs {
		if c.Depth < depth {
			c.ReplyURL = postURL + "?replytocom=" + strconv.FormatInt(c.ID, 10) + "#respond"
		}
		setReplyURLs(c.Children, postURL, depth)
	}
}

// loadComments fills d.Comments for posts and pages. query is the normalised query (raw when the cache is
// bypassed). It reports whether the page now holds per-visitor content, which must not be cached.
func (st *Site) loadComments(ctx context.Context, snap *snapshot, r *http.Request, d *theme.PageData, query string) (bool, error) {
	switch d.Kind {
	case theme.KindSingle, theme.KindPage, theme.KindFrontPage:
	default:
		return false, nil
	}
	disc := snap.settings.Discussion
	if d.Post == nil || !disc.CommentsEnabled {
		return false, nil
	}
	now := st.now()
	p := d.Post
	pub := p.PublishedAt
	t, err := st.loadThread(ctx, p.ID, disc.ThreadDepth)
	if err != nil {
		return false, err
	}
	c := &theme.Comments{Open: disc.OpenFor(p.CommentStatus, &pub, now), Total: len(t.byID)}
	q, _ := url.ParseQuery(query)

	threads := t.roots
	if per := disc.CommentsPerPage; per > 0 && len(threads) > per {
		pages := (len(threads) + per - 1) / per
		cur, _ := strconv.Atoi(q.Get("cpage"))
		cur = min(max(cur, 1), pages)
		threads = threads[(cur-1)*per : min(cur*per, len(threads))]
		c.Pagination = theme.NewPagination(cur, pages, func(n int) string {
			if n == 1 {
				return p.URL + "#comments"
			}
			return p.URL + "?cpage=" + strconv.Itoa(n) + "#comments"
		})
	}
	c.Threads = threads

	personal := false
	if c.Open {
		setReplyURLs(c.Threads, p.URL, disc.ThreadDepth)
		c.Form = &theme.CommentForm{Action: snap.abs(CommentsPath), PostID: p.ID, Honeypot: HoneypotField,
			Token: st.cfg.CommentSigner.FormToken(p.ID, now), CancelURL: p.URL + "#respond"}
		if id, err := strconv.ParseInt(q.Get("replytocom"), 10, 64); err == nil {
			personal = true
			c.Form.ReplyTo = t.byID[id]
		}
	}
	if ck, err := r.Cookie(NoticeCookie); err == nil {
		if id, ok := st.cfg.CommentSigner.ParseNoticeCookie(ck.Value, now); ok {
			n, err := st.pendingComment(ctx, p.ID, id)
			if err != nil {
				return false, err
			}
			c.Notice, personal = n, personal || n != nil
		}
	}
	d.Comments = c
	return personal, nil
}

func (st *Site) pendingComment(ctx context.Context, postID, id int64) (*theme.Comment, error) {
	var r commentRow
	err := st.db.QueryRow(ctx, `
SELECT id, coalesce(parent_id, 0), author_name, author_url, body, created_at, coalesce(legacy_key, '')
FROM comments WHERE id = $1 AND post_id = $2 AND status = 'pending'`, id, postID).
		Scan(&r.id, &r.parentID, &r.name, &r.url, &r.body, &r.created, &r.legacyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v := r.view(1)
	v.Pending = true
	return v, nil
}

// CommentLocation is where POST /comments sends the visitor: the post, on the comment page holding the
// comment's thread, at its anchor. A pending comment shows as the notice, so its anchor is on page one.
func (st *Site) CommentLocation(ctx context.Context, postID, commentID int64, status string) (string, error) {
	refs, err := st.postRefs(ctx, []int64{postID})
	if err != nil || refs[postID] == nil {
		return "", err
	}
	snap := st.snap.Load()
	postURL := snap.abs(snap.refPath(refs[postID]))
	anchor := "#comment-" + strconv.FormatInt(commentID, 10)
	if status != "approved" {
		return postURL + anchor, nil
	}
	disc := snap.settings.Discussion
	t, err := st.loadThread(ctx, postID, disc.ThreadDepth)
	if err != nil {
		return "", err
	}
	if v := t.byID[commentID]; v != nil {
		anchor = "#" + v.Anchor
		if per := disc.CommentsPerPage; per > 0 {
			if page := t.root[commentID]/per + 1; page > 1 {
				return postURL + "?cpage=" + strconv.Itoa(page) + anchor, nil
			}
		}
	}
	return postURL + anchor, nil
}
