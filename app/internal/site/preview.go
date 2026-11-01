package site

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/content"
	"afsc/internal/theme"
)

// PreviewTTL is how long a preview link works.
const PreviewTTL = time.Hour

// PreviewKey derives the preview signing key from AFSC_JWT_SECRET, so no new secret is needed (Q12).
func PreviewKey(jwtSecret []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, jwtSecret, nil, "afsc-preview", 32)
}

var randomPreviewKey = sync.OnceValue(func() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
})

func (st *Site) previewKey() []byte {
	if len(st.cfg.PreviewKey) > 0 {
		return st.cfg.PreviewKey
	}
	return randomPreviewKey()
}

func (st *Site) previewMAC(msg []byte) []byte {
	m := hmac.New(sha256.New, st.previewKey())
	m.Write(msg)
	return m.Sum(nil)
}

// PreviewToken signs (post id, revision id, expiry). Revision 0 previews the post's saved content.
func (st *Site) PreviewToken(postID, revisionID int64, exp time.Time) string {
	msg := make([]byte, 24)
	binary.BigEndian.PutUint64(msg[0:], uint64(postID))
	binary.BigEndian.PutUint64(msg[8:], uint64(revisionID))
	binary.BigEndian.PutUint64(msg[16:], uint64(exp.Unix()))
	return base64.RawURLEncoding.EncodeToString(append(msg, st.previewMAC(msg)...))
}

// PreviewURL is a preview link valid for PreviewTTL.
func (st *Site) PreviewURL(postID, revisionID int64) (string, time.Time) {
	exp := st.now().Add(PreviewTTL).Truncate(time.Second)
	return st.cfg.BaseURL + "/?preview=" + st.PreviewToken(postID, revisionID, exp), exp
}

func (st *Site) parsePreview(tok string) (postID, revisionID int64, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(b) != 24+sha256.Size || !hmac.Equal(b[24:], st.previewMAC(b[:24])) {
		return 0, 0, false
	}
	if st.now().Unix() > int64(binary.BigEndian.Uint64(b[16:24])) {
		return 0, 0, false
	}
	return int64(binary.BigEndian.Uint64(b[0:8])), int64(binary.BigEndian.Uint64(b[8:16])), true
}

// servePreview renders /?preview=<token> outside the page cache; a bad or expired token is a 404.
func (st *Site) servePreview(w http.ResponseWriter, r *http.Request, snap *snapshot) {
	resp, err := st.renderPreview(r, snap)
	if err == nil && resp == nil {
		resp, err = st.previewPage(r, snap, st.basePage(snap, r, theme.Kind404), http.StatusNotFound)
	}
	if resp != nil {
		resp.header.Set("Cache-Control", "no-store")
		resp.header.Set("X-Robots-Tag", "noindex")
		resp.header.Set("Referrer-Policy", "no-referrer")
	}
	st.finish(w, r, resp, err)
}

func (st *Site) renderPreview(r *http.Request, snap *snapshot) (*response, error) {
	postID, revID, ok := st.parsePreview(r.URL.Query().Get("preview"))
	if !ok {
		return nil, nil
	}
	ctx := r.Context()
	rows, err := st.postsByID(ctx, []int64{postID})
	if err != nil || len(rows) == 0 || rows[0].ref.Status == "trash" {
		return nil, err
	}
	p := rows[0]
	if err := st.hydrate(ctx, snap, rows, hydrateOpts{}); err != nil {
		return nil, err
	}
	v, raw := p.view, p.doc
	if revID != 0 {
		err := st.db.QueryRow(ctx, `SELECT title, excerpt, content FROM post_revisions WHERE id = $1 AND post_id = $2`,
			revID, postID).Scan(&v.Title, &v.Excerpt, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	doc, err := content.Parse(raw)
	if err != nil {
		return nil, err
	}
	h, text, err := st.RenderContent(ctx, p.ref.ID, p.ref.LegacyKey, doc)
	if err != nil {
		return nil, err
	}
	v.ContentHTML, v.ContentText = template.HTML(h), text
	kind := theme.KindSingle
	if p.ref.Type == "page" {
		kind = theme.KindPage
	}
	d := st.basePage(snap, r, kind)
	d.Post = v
	d.Request.Path, d.Request.URL = snap.refPath(&p.ref), v.URL
	return st.previewPage(r, snap, d, http.StatusOK)
}

func (st *Site) previewPage(r *http.Request, snap *snapshot, d *theme.PageData, status int) (*response, error) {
	d.Request.NoIndex = true
	if err := st.buildHead(r.Context(), snap, d); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := st.theme.Page(&buf, d); err != nil {
		return nil, err
	}
	return &response{status: status, header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, body: buf.Bytes()}, nil
}

// RenderContent renders a post's document the way stored content_html is rendered: theme partials, image sizes
// and the post's WordPress id.
func (st *Site) RenderContent(ctx context.Context, postID int64, legacyKey string, doc *content.Node) (string, string, error) {
	media, err := st.loadMedia(ctx, content.MediaIDs(doc))
	if err != nil {
		return "", "", err
	}
	rc := content.RenderContext{PostID: wpID(postID, legacyKey, "post"), Images: st.ImageSizes(),
		Media: func(id int64) (content.MediaInfo, bool) {
			m, ok := media[id]
			return m.MediaInfo, ok
		}}
	if st.theme.HasPartials() {
		rc.Partials = st.theme
	}
	return content.Render(doc, rc)
}

func (st *Site) ImageSizes() *content.ImageSizes { return st.theme.Manifest().Images() }

// AllowedClasses are the theme's content classes, which saves validate against.
func (st *Site) AllowedClasses() map[string]bool { return st.theme.Manifest().AllowedClasses() }

// PostURL is the absolute URL ref would have, using its own slug, parent, date and primary category.
func (st *Site) PostURL(ref *PostRef) string {
	s := st.snap.Load()
	return s.abs(s.refPath(ref))
}

// VisibleURLs maps each of ids that the public site shows to its absolute URL.
func (st *Site) VisibleURLs(ctx context.Context, ids []int64) (map[int64]string, error) {
	refs, err := st.postRefs(ctx, ids)
	if err != nil {
		return nil, err
	}
	s, now := st.snap.Load(), st.now()
	out := map[int64]string{}
	for id, ref := range refs {
		if ref.Visible(now) {
			out[id] = s.abs(s.refPath(ref))
		}
	}
	return out, nil
}
