package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"afsc/internal/content"
	"afsc/internal/settings"
	"afsc/internal/storage"
)

type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error)
}

type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

const uploadWorkers = 8

type writer struct {
	conn   beginner
	shared pgx.Tx // set in dry-run: every phase runs in this one transaction, rolled back at the end
	dir    string
	ix     *index
	rep    *Report
	store  storage.Storage
	logf   func(string, ...any)
	like   string
	parts  content.Partials
	images *content.ImageSizes

	users     map[string]uuid.UUID
	media     map[string]int64
	mediaInfo map[int64]content.MediaInfo
	authors   map[string]int64
	terms     map[string]int64
	posts     map[string]int64
	menus     map[string]int64
}

func (w *writer) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if w.shared != nil {
		return fn(w.shared)
	}
	tx, err := w.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *writer) run(ctx context.Context) error {
	phases := []struct {
		name string
		fn   func(context.Context, pgx.Tx) error
	}{
		{"users", w.writeUsers}, {"media", w.writeMedia}, {"authors", w.writeAuthors}, {"terms", w.writeTerms},
		{"posts", w.writePosts}, {"post relations", w.writePostRelations}, {"comments", w.writeComments},
		{"menus", w.writeMenus}, {"redirects", w.writeRedirects}, {"legacy records", w.writeLegacy},
		{"settings", w.writeSettings},
	}
	for _, p := range phases {
		start := time.Now()
		if err := w.inTx(ctx, func(tx pgx.Tx) error { return p.fn(ctx, tx) }); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		w.logf("%s done in %s", p.name, time.Since(start).Round(time.Millisecond))
	}
	return nil
}

func doc(v map[string]any) ([]byte, error) { return json.Marshal(v) }

func createStage(ctx context.Context, tx pgx.Tx, table string) error {
	_, err := tx.Exec(ctx, "CREATE TEMP TABLE "+pgx.Identifier{table}.Sanitize()+" (doc jsonb NOT NULL) ON COMMIT DROP")
	return err
}

// stageFile streams records of file through build into the temp table; build returns zero or more documents.
func stageFile[T any](ctx context.Context, tx pgx.Tx, dir, file, table string, build func(rec *T) ([][]byte, error)) (int, error) {
	rr, err := openRecords(dir, file)
	if err != nil {
		return 0, err
	}
	defer rr.Close()

	var pending [][]byte
	next := func() ([]any, error) {
		for len(pending) == 0 {
			var rec T
			ok, err := rr.next(&rec)
			if err != nil || !ok {
				return nil, err
			}
			if pending, err = build(&rec); err != nil {
				return nil, err
			}
		}
		d := pending[0]
		pending = pending[1:]
		return []any{json.RawMessage(d)}, nil
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{table}, []string{"doc"}, pgx.CopyFromFunc(next))
	return int(n), err
}

func one(d []byte, err error) ([][]byte, error) {
	if err != nil {
		return nil, err
	}
	return [][]byte{d}, nil
}

// upsert runs a RETURNING (xmax = 0) statement and adds its inserted/updated rows to entity's counts.
func (w *writer) upsert(ctx context.Context, tx dbtx, entity, sql string, args ...any) error {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	c := w.rep.count(entity)
	for rows.Next() {
		var inserted bool
		if err := rows.Scan(&inserted); err != nil {
			return err
		}
		if inserted {
			c.Created++
		} else {
			c.Updated++
		}
	}
	return rows.Err()
}

func (w *writer) finish(entity string, staged int) {
	c := w.rep.count(entity)
	c.Unchanged = max(0, staged-c.Created-c.Updated)
}

func (w *writer) deleted(entity string, tag pgconn.CommandTag) {
	w.rep.count(entity).Deleted += int(tag.RowsAffected())
}

func loadIDs(ctx context.Context, tx dbtx, query, like string) (map[string]int64, error) {
	rows, err := tx.Query(ctx, query, like)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var id int64
		if err := rows.Scan(&k, &id); err != nil {
			rows.Close()
			return nil, err
		}
		out[k] = id
	}
	return out, rows.Err()
}

func idsOf(m map[string]int64, keys func(yield func(string) bool)) []int64 {
	out := []int64{}
	for k := range keys {
		if id, ok := m[k]; ok {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func keysOf[V any](m map[string]V) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func maxDepth(entries map[string]*parented) int {
	d := 0
	for _, e := range entries {
		d = max(d, e.depth)
	}
	return d
}

func ptrID(m map[string]int64, key *string) any {
	if key == nil {
		return nil
	}
	if id, ok := m[*key]; ok {
		return id
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- users

var usernameCleanRe = regexp.MustCompile(`[^a-z0-9_.-]+`)

func uniqueUsername(email string, taken map[string]bool) string {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	base := strings.Trim(usernameCleanRe.ReplaceAllString(local, "-"), "-.")
	if len(base) > 28 {
		base = base[:28]
	}
	for len(base) < 3 {
		base += "_"
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = base + "-" + strconv.Itoa(i)
	}
	taken[name] = true
	return name
}

func (w *writer) writeUsers(ctx context.Context, tx pgx.Tx) error {
	// the insert path's CHECK constraints run even when ON CONFLICT ends up updating, so existing users
	// get their current username rather than an empty one
	taken, existing := map[string]bool{}, map[string]string{}
	rows, err := tx.Query(ctx, `SELECT username, coalesce(legacy_key, '') FROM users`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var u, k string
		if err := rows.Scan(&u, &k); err != nil {
			rows.Close()
			return err
		}
		taken[u] = true
		if k != "" {
			existing[k] = u
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if err := createStage(ctx, tx, "stage_users"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileUsers, "stage_users", func(r *UserRec) ([][]byte, error) {
		username, ok := existing[r.LegacyKey]
		if !ok {
			username = uniqueUsername(r.Email, taken)
		}
		return one(doc(map[string]any{
			"legacy_key": r.LegacyKey, "username": username, "email": strings.ToLower(strings.TrimSpace(r.Email)),
			"display_name": r.DisplayName, "role": r.Role, "disabled": r.Disabled,
		}))
	})
	if err != nil {
		return err
	}

	linked, err := tx.Query(ctx, sqlLinkUsers)
	if err != nil {
		return err
	}
	for linked.Next() {
		var k string
		if err := linked.Scan(&k); err != nil {
			linked.Close()
			return err
		}
		w.rep.count("users").Updated++
		w.rep.warnf(k, "email", "linked to the existing AFSC account with the same email; its role and password are kept")
	}
	if err := linked.Err(); err != nil {
		return err
	}
	if err := w.upsert(ctx, tx, "users", sqlUpsertUsers); err != nil {
		return err
	}
	w.finish("users", n)

	w.users = map[string]uuid.UUID{}
	rows, err = tx.Query(ctx, `SELECT legacy_key, id FROM users WHERE legacy_key LIKE $1`, w.like)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k string
		var id uuid.UUID
		if err := rows.Scan(&k, &id); err != nil {
			rows.Close()
			return err
		}
		w.users[k] = id
	}
	if err := rows.Err(); err != nil {
		return err
	}

	inv, err := tx.Query(ctx, sqlInvites, w.like)
	if err != nil {
		return err
	}
	defer inv.Close()
	for inv.Next() {
		var k, email string
		if err := inv.Scan(&k, &email); err != nil {
			return err
		}
		w.rep.Invites = append(w.rep.Invites, Invite{LegacyKey: k, Email: email, Command: "afsc user invite --email " + email})
	}
	return inv.Err()
}

// ---- media

type dbMedia struct {
	id         int64
	legacyKey  string
	storageKey string
	sha        string
}

type upload struct {
	key, path, mime string
}

var wpPostKeyRe = regexp.MustCompile(`^[a-z0-9]+:post:(\d+)$`)

func (w *writer) writeMedia(ctx context.Context, tx pgx.Tx) error {
	byKey, bySHA := map[string]dbMedia{}, map[string]dbMedia{}
	rows, err := tx.Query(ctx, `SELECT id, coalesce(legacy_key, ''), storage_key, encode(sha256, 'hex') FROM media`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m dbMedia
		if err := rows.Scan(&m.id, &m.legacyKey, &m.storageKey, &m.sha); err != nil {
			rows.Close()
			return err
		}
		bySHA[m.sha] = m
		if m.legacyKey != "" {
			byKey[m.legacyKey] = m
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	storageKeys := map[string]string{}
	dbAlias := map[string]int64{}
	var uploads []upload
	err = eachRecord(w.dir, fileMedia, func(_ int, r *MediaRec) error {
		if w.ix.media[r.LegacyKey].alias != "" {
			return nil
		}
		if m, ok := byKey[r.LegacyKey]; ok && m.sha == r.SHA256 {
			storageKeys[r.LegacyKey] = m.storageKey
			return nil
		}
		if m, ok := bySHA[r.SHA256]; ok {
			dbAlias[r.LegacyKey] = m.id
			w.rep.warnf(r.LegacyKey, "sha256", "the same file is already stored as media %d; references will point at it", m.id)
			return nil
		}
		if _, ok := byKey[r.LegacyKey]; ok {
			w.rep.warnf(r.LegacyKey, "file", "file changed; the previous object stays in storage until cleaned up")
		}
		key, err := storage.NewKey()
		if err != nil {
			return err
		}
		if w.shared != nil {
			key = "dry-run/" + r.SHA256
		}
		storageKeys[r.LegacyKey] = key
		uploads = append(uploads, upload{key: key, path: filepath.Join(w.dir, filepath.FromSlash(r.File)), mime: r.Mime})
		return nil
	})
	if err != nil {
		return err
	}
	if w.shared == nil {
		if err := w.upload(ctx, uploads); err != nil {
			return err
		}
	}

	if err := createStage(ctx, tx, "stage_media"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileMedia, "stage_media", func(r *MediaRec) ([][]byte, error) {
		sk, ok := storageKeys[r.LegacyKey]
		if !ok {
			w.rep.count("media").Skipped++
			return nil, nil
		}
		st, err := os.Stat(filepath.Join(w.dir, filepath.FromSlash(r.File)))
		if err != nil {
			return nil, err
		}
		visible := true
		if r.LibraryVisible != nil {
			visible = *r.LibraryVisible
		}
		created := w.ix.generatedAt
		if r.CreatedAt != nil {
			created = *r.CreatedAt
		}
		return one(doc(map[string]any{
			"legacy_key": r.LegacyKey, "storage_key": sk, "filename": r.Filename, "mime": r.Mime, "bytes": st.Size(),
			"sha256": r.SHA256, "width": r.Width, "height": r.Height, "duration_seconds": r.DurationSeconds,
			"title": r.Title, "alt": r.Alt, "caption": r.Caption, "description": r.Description,
			"library_visible": visible, "created_at": created,
		}))
	})
	if err != nil {
		return err
	}
	if err := w.upsert(ctx, tx, "media", sqlUpsertMedia); err != nil {
		return err
	}
	c := w.rep.count("media")
	c.Unchanged = max(0, n-c.Created-c.Updated)

	if err := w.loadMedia(ctx, tx, dbAlias); err != nil {
		return err
	}
	return w.writeLegacyPaths(ctx, tx)
}

func (w *writer) upload(ctx context.Context, uploads []upload) error {
	if w.store == nil {
		return fmt.Errorf("object storage is not configured")
	}
	w.logf("uploading %d files", len(uploads))
	jobs := make(chan upload)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	for range uploadWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				if err := w.put(ctx, u); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, u := range uploads {
		mu.Lock()
		failed := first != nil
		mu.Unlock()
		if failed {
			break
		}
		jobs <- u
	}
	close(jobs)
	wg.Wait()
	return first
}

func (w *writer) put(ctx context.Context, u upload) error {
	f, err := os.Open(u.path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	return w.store.Put(ctx, u.key, f, st.Size(), u.mime)
}

func (w *writer) loadMedia(ctx context.Context, tx pgx.Tx, dbAlias map[string]int64) error {
	aliasIDs := make([]int64, 0, len(dbAlias))
	for _, id := range dbAlias {
		aliasIDs = append(aliasIDs, id)
	}
	rows, err := tx.Query(ctx, `
    SELECT id, coalesce(legacy_key, ''), filename, substr(encode(sha256, 'hex'), 1, 8), mime,
           coalesce(width, 0), coalesce(height, 0)
    FROM media WHERE legacy_key LIKE $1 OR id = ANY($2::bigint[])`, w.like, aliasIDs)
	if err != nil {
		return err
	}
	w.media, w.mediaInfo = map[string]int64{}, map[int64]content.MediaInfo{}
	for rows.Next() {
		var m content.MediaInfo
		var key string
		if err := rows.Scan(&m.ID, &key, &m.Filename, &m.Version, &m.Mime, &m.Width, &m.Height); err != nil {
			rows.Close()
			return err
		}
		if sm := wpPostKeyRe.FindStringSubmatch(key); sm != nil {
			m.LegacyID, _ = strconv.ParseInt(sm[1], 10, 64)
		}
		if key != "" {
			w.media[key] = m.ID
		}
		w.mediaInfo[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for key, id := range dbAlias {
		w.media[key] = id
	}
	for key, e := range w.ix.media {
		if e.alias != "" {
			if id, ok := w.media[e.alias]; ok {
				w.media[key] = id
			}
		}
	}
	return nil
}

func (w *writer) writeLegacyPaths(ctx context.Context, tx pgx.Tx) error {
	if err := createStage(ctx, tx, "stage_paths"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileMedia, "stage_paths", func(r *MediaRec) ([][]byte, error) {
		var docs [][]byte
		for _, p := range r.LegacyPaths {
			d, err := doc(map[string]any{
				"path": p.Path, "media_id": w.media[r.LegacyKey], "variant": p.Variant,
				"width": p.Width, "height": p.Height, "crop": p.Crop,
			})
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		return docs, nil
	})
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, sqlDeleteStalePaths, idsOf(w.media, keysOf(w.ix.media)))
	if err != nil {
		return err
	}
	w.deleted("media_legacy_paths", tag)
	if err := w.upsert(ctx, tx, "media_legacy_paths", sqlUpsertPaths); err != nil {
		return err
	}
	w.finish("media_legacy_paths", n)
	return nil
}

// ---- authors and terms

func (w *writer) writeAuthors(ctx context.Context, tx pgx.Tx) error {
	if err := createStage(ctx, tx, "stage_authors"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileAuthors, "stage_authors", func(r *AuthorRec) ([][]byte, error) {
		social := r.Social
		if social == nil {
			social = map[string]string{}
		}
		var user any
		if r.User != nil {
			user = w.users[*r.User].String()
		}
		return one(doc(map[string]any{
			"legacy_key": r.LegacyKey, "slug": r.Slug, "display_name": r.DisplayName, "bio": r.Bio, "website": r.Website,
			"social": social, "avatar_media_id": ptrID(w.media, r.AvatarMedia), "user_id": user,
		}))
	})
	if err != nil {
		return err
	}
	if err := w.upsert(ctx, tx, "authors", sqlUpsertAuthors); err != nil {
		return err
	}
	w.finish("authors", n)
	w.authors, err = loadIDs(ctx, tx, keyQueries["authors"], w.like)
	return err
}

func (w *writer) writeTerms(ctx context.Context, tx pgx.Tx) error {
	if err := createStage(ctx, tx, "stage_terms"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileTerms, "stage_terms", func(r *TermRec) ([][]byte, error) {
		return one(doc(map[string]any{
			"legacy_key": r.LegacyKey, "taxonomy": r.Taxonomy, "slug": r.Slug, "name": r.Name,
			"description": r.Description, "parent": r.Parent, "depth": w.ix.terms[r.LegacyKey].depth,
		}))
	})
	if err != nil {
		return err
	}
	for d := 0; d <= maxDepth(w.ix.terms); d++ {
		if err := w.upsert(ctx, tx, "terms", sqlUpsertTerms, d); err != nil {
			return err
		}
	}
	w.finish("terms", n)
	w.terms, err = loadIDs(ctx, tx, keyQueries["terms"], w.like)
	return err
}

// ---- posts

func (w *writer) resolveMedia(key string) (int64, bool) {
	id, ok := w.media[key]
	return id, ok
}

func (w *writer) mediaLookup(id int64) (content.MediaInfo, bool) {
	m, ok := w.mediaInfo[id]
	return m, ok
}

func (w *writer) writePosts(ctx context.Context, tx pgx.Tx) error {
	if err := createStage(ctx, tx, "stage_posts"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, filePosts, "stage_posts", func(r *PostRec) ([][]byte, error) {
		d, refIssues, res, err := prepareContent(r.Content, w.ix.allowedClasses, w.resolveMedia)
		if err != nil || len(refIssues) > 0 || !res.OK() {
			return nil, fmt.Errorf("%s: content no longer validates", r.LegacyKey)
		}
		var postID int64
		if sm := wpPostKeyRe.FindStringSubmatch(r.LegacyKey); sm != nil {
			postID, _ = strconv.ParseInt(sm[1], 10, 64)
		}
		html, text, err := content.Render(d, content.RenderContext{PostID: postID, Media: w.mediaLookup, Partials: w.parts,
			Images: w.images})
		if err != nil {
			return nil, fmt.Errorf("%s: render: %w", r.LegacyKey, err)
		}
		updated := *r.CreatedAt
		if r.UpdatedAt != nil {
			updated = *r.UpdatedAt
		}
		commentStatus := r.CommentStatus
		if commentStatus == "" {
			commentStatus = "open"
		}
		return one(doc(map[string]any{
			"legacy_key": r.LegacyKey, "type": r.Type, "status": r.Status, "slug": r.Slug, "parent": r.Parent,
			"title": r.Title, "excerpt": r.Excerpt, "content": d, "content_html": html, "content_text": text,
			"template": r.Template, "featured_media_id": ptrID(w.media, r.FeaturedMedia), "published_at": r.PublishedAt,
			"sticky": r.Sticky, "menu_order": r.MenuOrder, "comment_status": commentStatus, "feed_guid": r.FeedGUID,
			"created_at": r.CreatedAt, "updated_at": updated, "depth": w.ix.posts[r.LegacyKey].depth,
		}))
	})
	if err != nil {
		return err
	}
	for d := 0; d <= maxDepth(w.ix.posts); d++ {
		if err := w.upsert(ctx, tx, "posts", sqlUpsertPosts, d); err != nil {
			return err
		}
	}
	w.finish("posts", n)
	w.posts, err = loadIDs(ctx, tx, keyQueries["posts"], w.like)
	return err
}

func seoDoc(entityType string, id int64, s *SEORec, media map[string]int64) ([]byte, error) {
	return doc(map[string]any{
		"entity_type": entityType, "entity_id": id, "title": s.Title, "description": s.Description,
		"canonical": s.Canonical, "robots_noindex": s.RobotsNoindex, "robots_nofollow": s.RobotsNofollow,
		"robots_advanced": s.RobotsAdvanced, "og_title": s.OGTitle, "og_description": s.OGDescription,
		"og_image_media_id": ptrID(media, s.OGImageMedia), "twitter_title": s.TwitterTitle,
		"twitter_description": s.TwitterDescription, "twitter_image_media_id": ptrID(media, s.TwitterImageMedia),
		"focus_keyphrase": s.FocusKeyphrase, "breadcrumb_title": s.BreadcrumbTitle, "is_cornerstone": s.IsCornerstone,
	})
}

func (s *SEORec) empty() bool {
	return s == nil || (s.Title == nil && s.Description == nil && s.Canonical == nil && s.RobotsNoindex == nil &&
		s.RobotsNofollow == nil && s.RobotsAdvanced == nil && s.OGTitle == nil && s.OGDescription == nil &&
		s.OGImageMedia == nil && s.TwitterTitle == nil && s.TwitterDescription == nil && s.TwitterImageMedia == nil &&
		s.FocusKeyphrase == nil && s.BreadcrumbTitle == nil && !s.IsCornerstone)
}

func (w *writer) writePostRelations(ctx context.Context, tx pgx.Tx) error {
	for _, t := range []string{"stage_post_authors", "stage_post_terms", "stage_seo", "stage_videos"} {
		if err := createStage(ctx, tx, t); err != nil {
			return err
		}
	}
	postIDs := idsOf(w.posts, keysOf(w.ix.posts))

	if _, err := stageFile(ctx, tx, w.dir, filePosts, "stage_post_authors", func(r *PostRec) ([][]byte, error) {
		var docs [][]byte
		for i, a := range r.Authors {
			d, err := doc(map[string]any{"post_id": w.posts[r.LegacyKey], "author_id": w.authors[a], "position": i})
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		return docs, nil
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, sqlDeleteStalePostAuthors, postIDs); err != nil {
		return err
	}
	if err := w.upsert(ctx, tx, "post_authors", sqlUpsertPostAuthors); err != nil {
		return err
	}

	if _, err := stageFile(ctx, tx, w.dir, filePosts, "stage_post_terms", func(r *PostRec) ([][]byte, error) {
		var docs [][]byte
		for _, t := range r.Terms {
			d, err := doc(map[string]any{"post_id": w.posts[r.LegacyKey], "term_id": w.terms[t.Term], "is_primary": t.Primary})
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		return docs, nil
	}); err != nil {
		return err
	}
	for _, q := range []string{sqlDeleteStalePostTerms, sqlClearMovedPrimaries} {
		if _, err := tx.Exec(ctx, q, postIDs); err != nil {
			return err
		}
	}
	if err := w.upsert(ctx, tx, "post_terms", sqlUpsertPostTerms); err != nil {
		return err
	}

	seoTotal := 0
	for _, src := range []struct {
		file, entity string
		ids          map[string]int64
	}{{filePosts, "post", w.posts}, {fileTerms, "term", w.terms}, {fileAuthors, "author", w.authors}} {
		var n int
		var err error
		switch src.file {
		case filePosts:
			n, err = stageFile(ctx, tx, w.dir, src.file, "stage_seo", func(r *PostRec) ([][]byte, error) {
				if r.SEO.empty() {
					return nil, nil
				}
				return one(seoDoc(src.entity, src.ids[r.LegacyKey], r.SEO, w.media))
			})
		case fileTerms:
			n, err = stageFile(ctx, tx, w.dir, src.file, "stage_seo", func(r *TermRec) ([][]byte, error) {
				if r.SEO.empty() {
					return nil, nil
				}
				return one(seoDoc(src.entity, src.ids[r.LegacyKey], r.SEO, w.media))
			})
		case fileAuthors:
			n, err = stageFile(ctx, tx, w.dir, src.file, "stage_seo", func(r *AuthorRec) ([][]byte, error) {
				if r.SEO.empty() {
					return nil, nil
				}
				return one(seoDoc(src.entity, src.ids[r.LegacyKey], r.SEO, w.media))
			})
		}
		if err != nil {
			return err
		}
		seoTotal += n
	}
	tag, err := tx.Exec(ctx, sqlDeleteStaleSEO, postIDs, idsOf(w.terms, keysOf(w.ix.terms)), idsOf(w.authors, keysOf(w.ix.authors)))
	if err != nil {
		return err
	}
	w.deleted("seo", tag)
	if err := w.upsert(ctx, tx, "seo", sqlUpsertSEO); err != nil {
		return err
	}
	w.finish("seo", seoTotal)

	n, err := stageFile(ctx, tx, w.dir, filePosts, "stage_videos", func(r *PostRec) ([][]byte, error) {
		var docs [][]byte
		for _, v := range r.Videos {
			d, err := doc(map[string]any{
				"post_id": w.posts[r.LegacyKey], "provider": v.Provider, "external_id": v.ExternalID, "url": v.URL,
				"title": v.Title, "description": v.Description, "thumbnail_url": v.ThumbnailURL,
				"thumbnail_media_id": ptrID(w.media, v.ThumbnailMedia), "duration_seconds": v.DurationSeconds,
				"upload_date": v.UploadDate, "family_friendly": v.FamilyFriendly,
			})
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		return docs, nil
	})
	if err != nil {
		return err
	}
	tag, err = tx.Exec(ctx, sqlDeleteStaleVideos, postIDs)
	if err != nil {
		return err
	}
	w.deleted("post_videos", tag)
	if err := w.upsert(ctx, tx, "post_videos", sqlUpsertVideos); err != nil {
		return err
	}
	w.finish("post_videos", n)
	_, err = tx.Exec(ctx, sqlSyncPostMedia, postIDs)
	return err
}

// ---- comments, menus, redirects, legacy, settings

func (w *writer) writeComments(ctx context.Context, tx pgx.Tx) error {
	if err := createStage(ctx, tx, "stage_comments"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileComments, "stage_comments", func(r *CommentRec) ([][]byte, error) {
		var user any
		if r.User != nil {
			user = w.users[*r.User].String()
		}
		return one(doc(map[string]any{
			"legacy_key": r.LegacyKey, "post_id": w.posts[r.Post], "parent": r.Parent, "author_name": r.AuthorName,
			"author_email": strings.TrimSpace(r.AuthorEmail), "author_url": r.AuthorURL, "user_id": user,
			"ip": nullable(r.IP), "user_agent": nullable(r.UserAgent), "body": r.Body, "status": r.Status,
			"created_at": r.CreatedAt, "depth": w.ix.comments[r.LegacyKey].depth,
		}))
	})
	if err != nil {
		return err
	}
	var ipDays int
	if err := tx.QueryRow(ctx, sqlIPRetentionDays, settings.Defaults().Discussion.CommentIPRetentionDays).Scan(&ipDays); err != nil {
		return err
	}
	for d := 0; d <= maxDepth(w.ix.comments); d++ {
		if err := w.upsert(ctx, tx, "comments", sqlUpsertComments, d, ipDays); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, sqlBumpCommentSeq, w.like); err != nil {
		return err
	}
	w.finish("comments", n)
	return nil
}

func (w *writer) writeMenus(ctx context.Context, tx pgx.Tx) error {
	if err := createStage(ctx, tx, "stage_menus"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileMenus, "stage_menus", func(r *MenuRec) ([][]byte, error) {
		return one(doc(map[string]any{"legacy_key": r.LegacyKey, "slug": r.Slug, "name": r.Name}))
	})
	if err != nil {
		return err
	}
	if err := w.upsert(ctx, tx, "menus", sqlUpsertMenus); err != nil {
		return err
	}
	w.finish("menus", n)
	if w.menus, err = loadIDs(ctx, tx, keyQueries["menus"], w.like); err != nil {
		return err
	}

	err = eachRecord(w.dir, fileMenus, func(_ int, r *MenuRec) error {
		if loc := deref(r.Location); loc != "" {
			_, err := tx.Exec(ctx, sqlUpsertMenuLocation, loc, w.menus[r.LegacyKey])
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := createStage(ctx, tx, "stage_menu_items"); err != nil {
		return err
	}
	targets := map[string]map[string]int64{"post": w.posts, "term": w.terms, "author": w.authors}
	items, err := stageFile(ctx, tx, w.dir, fileMenus, "stage_menu_items", func(r *MenuRec) ([][]byte, error) {
		var docs [][]byte
		for _, it := range r.Items {
			classes := it.CSSClasses
			if classes == nil {
				classes = []string{}
			}
			var target any
			if it.Kind != "url" {
				target = ptrID(targets[it.Kind], it.Target)
			}
			d, err := doc(map[string]any{
				"legacy_key": it.LegacyKey, "menu_id": w.menus[r.LegacyKey], "parent": it.Parent, "position": it.Position,
				"label": it.Label, "kind": it.Kind, "target_id": target, "url": it.URL, "new_tab": it.NewTab,
				"css_classes": classes, "depth": w.ix.menuItems[it.LegacyKey].depth,
			})
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		return docs, nil
	})
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, sqlDeleteStaleMenuItems, idsOf(w.menus, keysOf(w.ix.menus)))
	if err != nil {
		return err
	}
	w.deleted("menu_items", tag)
	for d := 0; d <= maxDepth(w.ix.menuItems); d++ {
		if err := w.upsert(ctx, tx, "menu_items", sqlUpsertMenuItems, d); err != nil {
			return err
		}
	}
	w.finish("menu_items", items)
	return nil
}

func (w *writer) writeRedirects(ctx context.Context, tx pgx.Tx) error {
	foreign := map[string]bool{}
	rows, err := tx.Query(ctx, sqlForeignExactRedirects, w.like)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		foreign[s] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if err := createStage(ctx, tx, "stage_redirects"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileRedirects, "stage_redirects", func(r *RedirectRec) ([][]byte, error) {
		if r.Match == "exact" && foreign[r.Source] {
			w.rep.count("redirects").Skipped++
			w.rep.warnf(r.LegacyKey, "source", "an existing redirect not from this bundle already handles %s; skipped", r.Source)
			return nil, nil
		}
		return one(doc(map[string]any{
			"legacy_key": r.LegacyKey, "source": r.Source, "match": r.Match, "target": r.Target, "status": r.Status, "note": r.Note,
		}))
	})
	if err != nil {
		return err
	}
	if err := w.upsert(ctx, tx, "redirects", sqlUpsertRedirects); err != nil {
		return err
	}
	w.finish("redirects", n)
	return nil
}

func (w *writer) writeLegacy(ctx context.Context, tx pgx.Tx) error {
	if err := createStage(ctx, tx, "stage_legacy"); err != nil {
		return err
	}
	n, err := stageFile(ctx, tx, w.dir, fileLegacy, "stage_legacy", func(r *LegacyRec) ([][]byte, error) {
		return one(doc(map[string]any{"legacy_key": r.LegacyKey, "kind": r.Kind, "source": r.Source}))
	})
	if err != nil {
		return err
	}
	if err := w.upsert(ctx, tx, "legacy_records", sqlUpsertLegacy); err != nil {
		return err
	}
	w.finish("legacy_records", n)
	return nil
}

func (w *writer) writeSettings(ctx context.Context, tx pgx.Tx) error {
	all := w.ix.settings
	for name, ref := range all.Refs() {
		if *ref == nil {
			continue
		}
		resolved := **ref
		switch {
		case strings.HasSuffix(name, "_media"):
			resolved.ID = w.media[resolved.LegacyKey]
		default:
			resolved.ID = w.posts[resolved.LegacyKey]
		}
		*ref = &resolved
	}
	staged := 0
	for _, key := range settings.Keys {
		if !w.ix.settingsPresent[key] {
			continue
		}
		b, err := json.Marshal(all.Group(key))
		if err != nil {
			return err
		}
		if err := w.upsert(ctx, tx, "settings", sqlUpsertSetting, key, json.RawMessage(b)); err != nil {
			return err
		}
		staged++
	}
	w.finish("settings", staged)
	return nil
}
