package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/comments"
	"afsc/internal/jobs"
	"afsc/internal/plugins"
	"afsc/internal/spam"
)

const maxBulkComments = 500

type commentsAPI struct {
	d   *adminDeps
	svc *comments.Service
}

func (h *authHandler) registerComments(g *echo.Group, d *adminDeps) {
	a := &commentsAPI{d: d, svc: comments.New(d.pool, d.site, d.site.CommentSigner(), nil)}
	mod := authz.ModerateComments
	h.route(g, http.MethodGet, "/comments", mod, a.list)
	h.route(g, http.MethodPost, "/comments/bulk", mod, a.bulk)
	h.route(g, http.MethodPost, "/comments/:id/reply", mod, a.reply)
	h.route(g, http.MethodGet, "/spam/blocklist", authz.ManageSettings, a.getBlocklist)
	h.route(g, http.MethodPut, "/spam/blocklist", authz.ManageSettings, a.putBlocklist)
	h.route(g, http.MethodPost, "/spam/retrain", authz.ManageSettings, a.retrain)
}

type adminComment struct {
	ID          int64           `json:"id"`
	PostID      int64           `json:"post_id"`
	PostTitle   string          `json:"post_title"`
	PostType    string          `json:"post_type"`
	PostURL     string          `json:"post_url"` // empty when the post isn't public
	ParentID    *int64          `json:"parent_id"`
	ParentName  *string         `json:"parent_author"`
	AuthorName  string          `json:"author_name"`
	AuthorEmail string          `json:"author_email"`
	AuthorURL   string          `json:"author_url"`
	IP          *string         `json:"ip"`
	UserAgent   *string         `json:"user_agent"`
	Staff       bool            `json:"staff"`
	Body        string          `json:"body"`
	Status      string          `json:"status"`
	SpamScore   *float32        `json:"spam_score"`
	SpamReasons json.RawMessage `json:"spam_reasons"`
	CreatedAt   time.Time       `json:"created_at"`
}

type commentList struct {
	pageResponse[adminComment]
	Counts map[string]int64 `json:"counts"`
}

func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func (a *commentsAPI) list(c *echo.Context) error {
	ctx := c.Request().Context()
	status := c.QueryParam("status")
	if status == "" {
		status = "pending"
	}
	if !slices.Contains(comments.Statuses, status) {
		return fieldError(c, http.StatusBadRequest, "status", "status must be pending, approved, spam or trash")
	}
	postID, _ := strconv.ParseInt(c.QueryParam("post_id"), 10, 64)
	q := strings.TrimSpace(c.QueryParam("q"))
	if len(q) > 200 {
		q = q[:200]
	}
	page, size := parsePage(c)
	rows, err := a.d.pool.Query(ctx, `
SELECT c.id, c.post_id, p.title, p.type, c.parent_id, pc.author_name, c.author_name, c.author_email, c.author_url,
       host(c.ip), c.user_agent, c.user_id IS NOT NULL, c.body, c.status, c.spam_score, coalesce(c.spam_reasons, '[]'),
       c.created_at, count(*) OVER ()
FROM comments c
JOIN posts p ON p.id = c.post_id
LEFT JOIN comments pc ON pc.id = c.parent_id
WHERE c.status = $1 AND ($2::bigint = 0 OR c.post_id = $2)
  AND ($3 = '' OR c.body ILIKE $4 OR c.author_name ILIKE $4 OR c.author_email::text ILIKE $4 OR host(c.ip) = $3)
ORDER BY c.created_at DESC, c.id DESC
LIMIT $5 OFFSET $6`, status, postID, q, likePattern(q), size, (page-1)*size)
	if err != nil {
		return err
	}
	items := []adminComment{}
	var total int64
	var r adminComment
	var reasons []byte
	if _, err := pgx.ForEachRow(rows, []any{&r.ID, &r.PostID, &r.PostTitle, &r.PostType, &r.ParentID, &r.ParentName,
		&r.AuthorName, &r.AuthorEmail, &r.AuthorURL, &r.IP, &r.UserAgent, &r.Staff, &r.Body, &r.Status, &r.SpamScore,
		&reasons, &r.CreatedAt, &total}, func() error {
		x := r
		x.SpamReasons = json.RawMessage(slices.Clone(reasons))
		items = append(items, x)
		r = adminComment{} // fresh pointer fields for the next row
		return nil
	}); err != nil {
		return err
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.PostID)
	}
	urls, err := a.d.site.CommentPostURLs(ctx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].PostURL = urls[items[i].PostID]
	}

	counts := map[string]int64{"pending": 0, "approved": 0, "spam": 0, "trash": 0}
	crows, err := a.d.pool.Query(ctx, `SELECT status, count(*) FROM comments WHERE $1::bigint = 0 OR post_id = $1 GROUP BY status`, postID)
	if err != nil {
		return err
	}
	var st string
	var n int64
	if _, err := pgx.ForEachRow(crows, []any{&st, &n}, func() error {
		counts[st] = n
		return nil
	}); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, commentList{
		pageResponse: pageResponse[adminComment]{Items: items, Page: page, PageSize: size, Total: total},
		Counts:       counts,
	})
}

// bulkActions maps a moderation action to the status it sets (Q9: not_spam approves).
var bulkActions = map[string]string{
	"approve": "approved", "unapprove": "pending", "spam": "spam", "not_spam": "approved", "trash": "trash",
	"restore": "pending",
}

func (a *commentsAPI) bulk(c *echo.Context) error {
	var in struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	if len(in.IDs) == 0 || len(in.IDs) > maxBulkComments {
		return fieldError(c, http.StatusBadRequest, "ids", fmt.Sprintf("send 1 to %d comment ids", maxBulkComments))
	}
	ctx := c.Request().Context()
	var changed []comments.Changed
	var err error
	if in.Action == "delete" {
		changed, err = a.svc.Delete(ctx, in.IDs)
		if errors.Is(err, comments.ErrNotDeletable) {
			return mediaJSONError(c, http.StatusConflict, err.Error(), "not_deletable", "ids", nil)
		}
		if err == nil {
			for _, ch := range changed {
				if err := audit.Write(ctx, a.d.pool, audit.Entry{Actor: actor(c), Action: "comment.delete",
					EntityType: "comment", EntityID: strconv.FormatInt(ch.ID, 10),
					Diff: map[string]any{"status": ch.From, "post_id": ch.PostID}, IP: clientIP(c)}); err != nil {
					return err
				}
			}
		}
	} else {
		status, ok := bulkActions[in.Action]
		if !ok {
			return fieldError(c, http.StatusBadRequest, "action",
				"action must be approve, unapprove, spam, not_spam, trash, restore or delete")
		}
		changed, err = a.svc.SetStatus(ctx, in.IDs, status)
	}
	if err != nil {
		return err
	}
	if changed == nil {
		changed = []comments.Changed{}
	}
	return c.JSON(http.StatusOK, map[string]any{"changed": changed})
}

func (a *commentsAPI) reply(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "comment not found")
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	res, err := a.svc.Reply(c.Request().Context(), currentUser(c), id, in.Body)
	var verr *comments.ValidationError
	switch {
	case errors.As(err, &verr):
		field, msg := firstField(verr)
		return fieldError(c, http.StatusBadRequest, field, msg)
	case errors.Is(err, comments.ErrNotFound):
		return jsonError(c, http.StatusNotFound, "comment not found")
	case err != nil:
		return err
	}
	return c.JSON(http.StatusCreated, map[string]any{"id": res.ID, "post_id": res.PostID, "parent_id": res.ParentID,
		"status": res.Status})
}

// firstField picks the alphabetically first bad field, so the error is stable across requests.
func firstField(v *comments.ValidationError) (string, string) {
	keys := make([]string, 0, len(v.Fields))
	for k := range v.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return "", "invalid comment"
	}
	return keys[0], v.Fields[keys[0]]
}

type blocklistResponse struct {
	Entries    map[string][]string `json:"entries"`
	Classifier classifierStats     `json:"classifier"`
}

type classifierStats struct {
	Spam   int  `json:"spam"`
	Ham    int  `json:"ham"`
	Min    int  `json:"min"`
	Active bool `json:"active"`
}

func (a *commentsAPI) blocklistResponse(c *echo.Context) error {
	ctx := c.Request().Context()
	list, err := spam.LoadBlocklist(ctx, a.d.pool)
	if err != nil {
		return err
	}
	stats, err := spam.Stats(ctx, a.d.pool)
	if err != nil {
		return err
	}
	out := blocklistResponse{Entries: map[string][]string{}, Classifier: classifierStats{Spam: stats.Spam, Ham: stats.Ham,
		Min: spam.MinMessages, Active: stats.Spam >= spam.MinMessages && stats.Ham >= spam.MinMessages}}
	for _, k := range spam.Kinds {
		out.Entries[k] = []string{}
	}
	for _, e := range list {
		out.Entries[e.Kind] = append(out.Entries[e.Kind], e.Value)
	}
	return c.JSON(http.StatusOK, out)
}

func (a *commentsAPI) getBlocklist(c *echo.Context) error { return a.blocklistResponse(c) }

func (a *commentsAPI) putBlocklist(c *echo.Context) error {
	var in struct {
		Entries map[string][]string `json:"entries"`
	}
	if err := decodeJSONLimit(c, &in, 1<<20); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	var entries []spam.Entry
	seen := map[spam.Entry]bool{}
	for kind, values := range in.Entries {
		if !slices.Contains(spam.Kinds, kind) {
			return fieldError(c, http.StatusBadRequest, "entries."+kind, "unknown blocklist kind")
		}
		for i, v := range values {
			norm, err := spam.NormalizeEntry(kind, v)
			if err != nil {
				return fieldError(c, http.StatusBadRequest, fmt.Sprintf("entries.%s[%d]", kind, i),
					fmt.Sprintf("%q %s", v, err.Error()))
			}
			e := spam.Entry{Kind: kind, Value: norm}
			if !seen[e] {
				seen[e] = true
				entries = append(entries, e)
			}
		}
	}
	if len(entries) > spam.MaxBlocklistEntries {
		return fieldError(c, http.StatusBadRequest, "entries", fmt.Sprintf("at most %d entries", spam.MaxBlocklistEntries))
	}
	ctx := c.Request().Context()
	tx, err := a.d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	added, removed, err := spam.ReplaceBlocklist(ctx, tx, entries)
	if err != nil {
		return err
	}
	if len(added)+len(removed) > 0 {
		fmtEntries := func(es []spam.Entry) []string {
			out := make([]string, 0, len(es))
			for _, e := range es {
				out = append(out, e.Kind+" "+e.Value)
			}
			return out
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "spam.blocklist", EntityType: "settings",
			EntityID: "spam_blocklist", Diff: map[string]any{"added": fmtEntries(added), "removed": fmtEntries(removed)},
			IP: clientIP(c)}); err != nil {
			return err
		}
		var kinds []string
		for _, e := range slices.Concat(added, removed) {
			if !slices.Contains(kinds, e.Kind) {
				kinds = append(kinds, e.Kind)
			}
		}
		slices.Sort(kinds)
		if err := plugins.Emit(ctx, a.d.plugins, tx, plugins.SettingsChanged,
			plugins.SettingsData{Group: "spam_blocklist", Fields: kinds}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return a.blocklistResponse(c)
}

func (a *commentsAPI) retrain(c *echo.Context) error {
	if err := jobs.Enqueue(c.Request().Context(), a.d.pool, "spam.retrain", nil, time.Time{}); err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, map[string]bool{"queued": true})
}
