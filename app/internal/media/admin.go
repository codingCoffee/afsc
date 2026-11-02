package media

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/audit"
	"afsc/internal/content"
	"afsc/internal/plugins"
	"afsc/internal/storage"
)

// Admin is the media library behind the admin API: upload, edit, replace, delete and the legacy path report.
type Admin struct {
	DB      *pgxpool.Pool
	Storage storage.Manager
	Images  func() *content.ImageSizes // the active theme's sizes, for thumbnail URLs; may return nil
	Logf    func(format string, args ...any)
	Plugins *plugins.Runtime // receives media.uploaded; may be nil
	BaseURL string
}

func (a *Admin) logf(format string, args ...any) {
	if a.Logf != nil {
		a.Logf(format, args...)
	}
}

// Error is a refusal the API shows as {message, field?, code?, ...Extra}.
type Error struct {
	Status  int
	Code    string
	Field   string
	Message string
	Extra   map[string]any
}

func (e *Error) Error() string { return e.Message }

func errf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

var errNotFound = &Error{Status: http.StatusNotFound, Message: "media not found"}

// Actor is who makes a change: for the audit log and the uploader-or-editor rule on edits.
type Actor struct {
	User   uuid.NullUUID
	IP     string
	Manage bool // content.manage: may edit anyone's uploads
}

func (act Actor) audit(ctx context.Context, tx pgx.Tx, action string, id int64, diff any) error {
	return audit.Write(ctx, tx, audit.Entry{Actor: act.User, Action: action, EntityType: "media",
		EntityID: strconv.FormatInt(id, 10), Diff: diff, IP: act.IP})
}

// Item is a library entry (MediaItem in the client types).
type Item struct {
	ID              int64     `json:"id"`
	Filename        string    `json:"filename"`
	Mime            string    `json:"mime"`
	Bytes           int64     `json:"bytes"`
	Width           *int      `json:"width"`
	Height          *int      `json:"height"`
	Title           string    `json:"title"`
	Alt             string    `json:"alt"`
	Caption         string    `json:"caption"`
	Description     string    `json:"description"`
	FocalX          *float64  `json:"focal_x"`
	FocalY          *float64  `json:"focal_y"`
	URL             string    `json:"url"`
	ThumbURL        *string   `json:"thumb_url"`
	LibraryVisible  bool      `json:"library_visible"`
	LegacyPathCount int64     `json:"legacy_path_count"`
	UploadedBy      *string   `json:"uploaded_by"`
	CreatedAt       time.Time `json:"created_at"`
	sha             string
	key             string
}

func (it *Item) info() content.MediaInfo {
	return content.MediaInfo{ID: it.ID, Filename: it.Filename, Version: it.sha[:8], Mime: it.Mime,
		Width: deref(it.Width), Height: deref(it.Height)}
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

type UsedIn struct {
	ID     int64  `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type LegacyPath struct {
	Path      string     `json:"path"`
	Variant   string     `json:"variant"`
	Hits      int64      `json:"hits"`
	LastHitAt *time.Time `json:"last_hit_at"`
}

// Detail is an item with where it is used and the legacy URLs that serve it (MediaDetail).
type Detail struct {
	Item
	UsedIn      []UsedIn     `json:"used_in"`
	LegacyPaths []LegacyPath `json:"legacy_paths"`
}

const (
	itemCols = `m.id, m.filename, m.mime, m.bytes, m.width, m.height, m.title, m.alt, m.caption, m.description,
  m.focal_x::float8, m.focal_y::float8, m.library_visible,
  (SELECT count(*) FROM media_legacy_paths lp WHERE lp.media_id = m.id), m.uploaded_by::text, m.created_at,
  encode(m.sha256, 'hex'), m.storage_key`
	sqlItem      = `SELECT ` + itemCols + ` FROM media m WHERE m.id = $1`
	sqlItemsPage = `
SELECT ` + itemCols + `, count(*) OVER () FROM media m
WHERE m.library_visible <> $1::bool
  AND ($2::text = '' OR m.filename ILIKE $2 OR m.title ILIKE $2 OR m.alt ILIKE $2)
  AND ($3::text = '' OR m.mime LIKE $3)
ORDER BY m.created_at DESC, m.id DESC
LIMIT $4 OFFSET $5`
	sqlUsedIn = `
SELECT p.id, p.type, p.title, p.status FROM post_media pm JOIN posts p ON p.id = pm.post_id
WHERE pm.media_id = $1 ORDER BY p.updated_at DESC, p.id DESC LIMIT 200`
	sqlUsedInIDs   = `SELECT post_id FROM post_media WHERE media_id = $1`
	sqlLegacyPaths = `
SELECT path, variant, hits, last_hit_at FROM media_legacy_paths WHERE media_id = $1 ORDER BY hits DESC, path`
	sqlInsertMedia = `
INSERT INTO media (storage_key, filename, mime, bytes, sha256, width, height, title, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (sha256) DO NOTHING
RETURNING id`
	sqlLegacyPage = `
SELECT path, media_id, hits, last_hit_at, count(*) OVER () FROM media_legacy_paths
ORDER BY hits DESC, path LIMIT $1 OFFSET $2`
)

func (a *Admin) scanItem(row pgx.Row, extra ...any) (*Item, error) {
	it := &Item{}
	dst := []any{&it.ID, &it.Filename, &it.Mime, &it.Bytes, &it.Width, &it.Height, &it.Title, &it.Alt, &it.Caption,
		&it.Description, &it.FocalX, &it.FocalY, &it.LibraryVisible, &it.LegacyPathCount, &it.UploadedBy,
		&it.CreatedAt, &it.sha, &it.key}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return nil, err
	}
	var sizes *content.ImageSizes
	if a.Images != nil {
		sizes = a.Images()
	}
	info := it.info()
	it.URL = content.MediaURL(info, "orig")
	it.ThumbURL = ThumbURL(info, sizes)
	return it, nil
}

// ThumbURL is a small variant for grids and avatars: the smallest srcset width below the original, else the
// original. Nil for anything but images.
func ThumbURL(info content.MediaInfo, sizes *content.ImageSizes) *string {
	if !strings.HasPrefix(info.Mime, "image/") {
		return nil
	}
	v := "orig"
	if content.Resizable(info.Mime) && sizes != nil {
		best := 0
		for _, w := range sizes.Widths {
			if w < info.Width && (best == 0 || w < best) {
				best = w
			}
		}
		if best > 0 {
			v = "w" + strconv.Itoa(best)
		}
	}
	u := content.MediaURL(info, v)
	return &u
}

func (a *Admin) item(ctx context.Context, q pgx.Tx, id int64) (*Item, error) {
	var row pgx.Row
	if q != nil {
		row = q.QueryRow(ctx, sqlItem+` FOR UPDATE OF m`, id)
	} else {
		row = a.DB.QueryRow(ctx, sqlItem, id)
	}
	it, err := a.scanItem(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	return it, err
}

type ListQuery struct {
	Q      string
	Type   string // image, video, audio, pdf
	Legacy bool   // only items hidden from the library (imported legacy-only files)
	Page   int
	Size   int
}

var typePatterns = map[string]string{"image": "image/%", "video": "video/%", "audio": "audio/%", "pdf": "application/pdf"}

func likePattern(q string) string {
	if q = strings.TrimSpace(q); q == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

func (a *Admin) List(ctx context.Context, q ListQuery) ([]Item, int64, error) {
	rows, err := a.DB.Query(ctx, sqlItemsPage, q.Legacy, likePattern(q.Q), typePatterns[q.Type], q.Size, (q.Page-1)*q.Size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Item{}
	var total int64
	for rows.Next() {
		it, err := a.scanItem(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *it)
	}
	return items, total, rows.Err()
}

func (a *Admin) Get(ctx context.Context, id int64) (*Detail, error) {
	it, err := a.item(ctx, nil, id)
	if err != nil {
		return nil, err
	}
	d := &Detail{Item: *it}
	rows, err := a.DB.Query(ctx, sqlUsedIn, id)
	if err != nil {
		return nil, err
	}
	if d.UsedIn, err = pgx.CollectRows(rows, pgx.RowToStructByPos[UsedIn]); err != nil {
		return nil, err
	}
	if d.LegacyPaths, err = a.legacyPaths(ctx, a.DB, id); err != nil {
		return nil, err
	}
	return d, nil
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (a *Admin) legacyPaths(ctx context.Context, q querier, id int64) ([]LegacyPath, error) {
	rows, err := q.Query(ctx, sqlLegacyPaths, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[LegacyPath])
}

func postIDs(ctx context.Context, q querier, id int64) ([]int64, error) {
	rows, err := q.Query(ctx, sqlUsedInIDs, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// needConfirm refuses a replace or delete that would change what legacy URLs serve, unless confirmed.
func (a *Admin) needConfirm(ctx context.Context, q querier, id int64, confirm bool) error {
	if confirm {
		return nil
	}
	paths, err := a.legacyPaths(ctx, q, id)
	if err != nil || len(paths) == 0 {
		return err
	}
	return &Error{Status: http.StatusConflict, Code: "legacy_paths",
		Message: fmt.Sprintf("%d legacy URLs serve this file; confirm to change them", len(paths)),
		Extra:   map[string]any{"legacy_paths": paths}}
}

func nullInt(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// Create stores an upload as a new library item. A file already in the library (same sha256) is not stored
// twice: the existing item comes back with created false.
func (a *Admin) Create(ctx context.Context, up Upload, act Actor) (*Item, bool, error) {
	o, err := a.put(ctx, up)
	if err != nil {
		return nil, false, err
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		a.remove(ctx, o.Key)
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	title := strings.TrimSuffix(o.Filename, extOf(o.Filename))
	var id int64
	err = tx.QueryRow(ctx, sqlInsertMedia, o.Key, o.Filename, o.Mime, o.Bytes, o.SHA256, nullInt(o.Width),
		nullInt(o.Height), title, act.User).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		a.remove(ctx, o.Key)
		if err := tx.QueryRow(ctx, `SELECT id FROM media WHERE sha256 = $1`, o.SHA256).Scan(&id); err != nil {
			return nil, false, err
		}
		it, err := a.item(ctx, nil, id)
		return it, false, err
	}
	if err == nil {
		err = act.audit(ctx, tx, "media.upload", id, map[string]any{"filename": o.Filename, "mime": o.Mime, "bytes": o.Bytes})
	}
	if err == nil {
		info := content.MediaInfo{ID: id, Filename: o.Filename, Version: hex.EncodeToString(o.SHA256)[:8], Mime: o.Mime,
			Width: o.Width, Height: o.Height}
		err = plugins.Emit(ctx, a.Plugins, tx, plugins.MediaUploaded, plugins.MediaData{ID: id, Mime: o.Mime,
			URL: a.BaseURL + content.MediaURL(info, "orig"), Width: nullInt(o.Width), Height: nullInt(o.Height),
			Filename: o.Filename})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		a.remove(ctx, o.Key)
		return nil, false, err
	}
	it, err := a.item(ctx, nil, id)
	return it, true, err
}

func extOf(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}

func (act Actor) mayEdit(it *Item) error {
	if act.Manage || (it.UploadedBy != nil && act.User.Valid && *it.UploadedBy == act.User.UUID.String()) {
		return nil
	}
	return &Error{Status: http.StatusForbidden, Code: "ownership", Message: "you can only change files you uploaded"}
}

// Patch edits a library item's text and focal point. Nil fields stay as they are; the focal point is set as a pair.
type Patch struct {
	Title       *string  `json:"title"`
	Alt         *string  `json:"alt"`
	Caption     *string  `json:"caption"`
	Description *string  `json:"description"`
	FocalX      *float64 `json:"focal_x"`
	FocalY      *float64 `json:"focal_y"`
}

func (p *Patch) validate() error {
	for _, f := range []struct {
		name string
		v    *string
		max  int
	}{{"title", p.Title, 500}, {"alt", p.Alt, 1000}, {"caption", p.Caption, 5000}, {"description", p.Description, 20000}} {
		if f.v != nil && utf8.RuneCountInString(*f.v) > f.max {
			return &Error{Status: http.StatusBadRequest, Field: f.name, Message: fmt.Sprintf("must be at most %d characters", f.max)}
		}
	}
	if (p.FocalX == nil) != (p.FocalY == nil) {
		return &Error{Status: http.StatusBadRequest, Field: "focal_x", Message: "set focal_x and focal_y together"}
	}
	for _, v := range []*float64{p.FocalX, p.FocalY} {
		if v != nil && (*v < 0 || *v > 1) {
			return &Error{Status: http.StatusBadRequest, Field: "focal_x", Message: "the focal point must be within 0-1"}
		}
	}
	return nil
}

// Update applies p and returns the item and the posts that show it, for purging.
func (a *Admin) Update(ctx context.Context, id int64, p Patch, act Actor) (*Item, []int64, error) {
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	it, err := a.item(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := act.mayEdit(it); err != nil {
		return nil, nil, err
	}
	diff := map[string]audit.Change{}
	for _, f := range []struct {
		name string
		cur  *string
		v    *string
	}{{"title", &it.Title, p.Title}, {"alt", &it.Alt, p.Alt}, {"caption", &it.Caption, p.Caption}, {"description", &it.Description, p.Description}} {
		if f.v != nil && *f.v != *f.cur {
			diff[f.name] = audit.Change{From: *f.cur, To: *f.v}
			*f.cur = *f.v
		}
	}
	if p.FocalX != nil {
		diff["focal"] = audit.Change{From: [2]*float64{it.FocalX, it.FocalY}, To: [2]float64{*p.FocalX, *p.FocalY}}
		it.FocalX, it.FocalY = p.FocalX, p.FocalY
	}
	if _, err := tx.Exec(ctx, `
UPDATE media SET title = $2, alt = $3, caption = $4, description = $5, focal_x = $6, focal_y = $7, updated_at = now()
WHERE id = $1`, id, it.Title, it.Alt, it.Caption, it.Description, it.FocalX, it.FocalY); err != nil {
		return nil, nil, err
	}
	if err := act.audit(ctx, tx, "media.update", id, diff); err != nil {
		return nil, nil, err
	}
	posts, err := postIDs(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return it, posts, nil
}

// Replace swaps the stored file and keeps the id, so content keeps pointing at it; the version in /m/ URLs
// changes and the old URLs redirect. The old object is deleted once the change is committed.
func (a *Admin) Replace(ctx context.Context, id int64, up Upload, confirm bool, act Actor) (*Item, []int64, error) {
	cur, err := a.item(ctx, nil, id)
	if err != nil {
		return nil, nil, err
	}
	if err := act.mayEdit(cur); err != nil {
		return nil, nil, err
	}
	if err := a.needConfirm(ctx, a.DB, id, confirm); err != nil {
		return nil, nil, err
	}
	o, err := a.put(ctx, up)
	if err != nil {
		return nil, nil, err
	}
	it, posts, oldKey, err := a.swap(ctx, id, o, confirm, act)
	if err != nil || oldKey == "" {
		a.remove(ctx, o.Key)
		return it, posts, err
	}
	a.remove(ctx, oldKey)
	return it, posts, nil
}

// swap points the row at the stored object o. oldKey is "" when nothing changed (the same file again).
func (a *Admin) swap(ctx context.Context, id int64, o *object, confirm bool, act Actor) (*Item, []int64, string, error) {
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	defer tx.Rollback(ctx)
	cur, err := a.item(ctx, tx, id)
	if err != nil {
		return nil, nil, "", err
	}
	if ClassOf(o.Mime) != ClassOf(cur.Mime) {
		return nil, nil, "", errf(http.StatusUnsupportedMediaType, "type", "replace a %s with another %s file",
			ClassOf(cur.Mime), ClassOf(cur.Mime))
	}
	if err := a.needConfirm(ctx, tx, id, confirm); err != nil {
		return nil, nil, "", err
	}
	if fmt.Sprintf("%x", o.SHA256) == cur.sha {
		return cur, nil, "", nil
	}
	_, err = tx.Exec(ctx, `
UPDATE media SET storage_key = $2, filename = $3, mime = $4, bytes = $5, sha256 = $6, width = $7, height = $8,
  updated_at = now()
WHERE id = $1`, id, o.Key, o.Filename, o.Mime, o.Bytes, o.SHA256, nullInt(o.Width), nullInt(o.Height))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		var other int64
		_ = a.DB.QueryRow(ctx, `SELECT id FROM media WHERE sha256 = $1`, o.SHA256).Scan(&other)
		return nil, nil, "", &Error{Status: http.StatusConflict, Code: "duplicate",
			Message: "this file is already in the library", Extra: map[string]any{"media_id": other}}
	}
	if err != nil {
		return nil, nil, "", err
	}
	diff := map[string]any{"from": map[string]any{"filename": cur.Filename, "sha256": cur.sha, "bytes": cur.Bytes},
		"to": map[string]any{"filename": o.Filename, "sha256": fmt.Sprintf("%x", o.SHA256), "bytes": o.Bytes}}
	if err := act.audit(ctx, tx, "media.replace", id, diff); err != nil {
		return nil, nil, "", err
	}
	posts, err := postIDs(ctx, tx, id)
	if err != nil {
		return nil, nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, "", err
	}
	it, err := a.item(ctx, nil, id)
	return it, posts, cur.key, err
}

// Delete removes an item and its stored file, and returns the posts that showed it.
func (a *Admin) Delete(ctx context.Context, id int64, confirm bool, act Actor) ([]int64, error) {
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	it, err := a.item(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := a.needConfirm(ctx, tx, id, confirm); err != nil {
		return nil, err
	}
	posts, err := postIDs(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media WHERE id = $1`, id); err != nil {
		return nil, err
	}
	if err := act.audit(ctx, tx, "media.delete", id, map[string]any{"filename": it.Filename, "sha256": it.sha,
		"legacy_paths": it.LegacyPathCount, "used_in": len(posts)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	a.remove(ctx, it.key)
	return posts, nil
}

type LegacyRow struct {
	Path      string     `json:"path"`
	MediaID   int64      `json:"media_id"`
	Hits      int64      `json:"hits"`
	LastHitAt *time.Time `json:"last_hit_at"`
}

// Legacy lists imported WordPress upload paths, most requested first.
func (a *Admin) Legacy(ctx context.Context, page, size int) ([]LegacyRow, int64, error) {
	rows, err := a.DB.Query(ctx, sqlLegacyPage, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []LegacyRow{}
	var total int64
	for rows.Next() {
		var r LegacyRow
		if err := rows.Scan(&r.Path, &r.MediaID, &r.Hits, &r.LastHitAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
