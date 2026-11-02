package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/mail"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"afsc/internal/content"
	"afsc/internal/sanitize"
	"afsc/internal/settings"
)

var (
	sourceRe   = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	slugRe     = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{Nd}_-]{1,200}$`)
	shaRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	templateRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	menuSlugRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	classRe    = regexp.MustCompile(`^[A-Za-z_-][A-Za-z0-9_-]{0,63}$`)
)

var (
	roles            = []string{"admin", "editor", "author", "contributor"}
	postStatuses     = []string{"draft", "pending", "scheduled", "published", "private", "trash"}
	commentStatuses  = []string{"pending", "approved", "spam", "trash"}
	redirectStatuses = []int{301, 302, 307, 308, 410, 451}
	menuKinds        = []string{"post", "term", "author", "url"}
	reservedPrefixes = []string{"/admin", "/m/"}
)

var allowedMimes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/avif": true,
	"image/svg+xml": true, "video/mp4": true, "video/webm": true, "application/pdf": true, "audio/mpeg": true,
}

type mediaEntry struct {
	sha   string
	alias string // canonical legacy key when another record has the same file
	fake  int64
}

type parented struct {
	parent string
	depth  int
	kind   string // post type, taxonomy, comment's post or item's menu
}

type index struct {
	source         string
	generatedAt    time.Time
	allowedClasses map[string]bool
	menuLocations  map[string]bool

	users       map[string]string
	media       map[string]*mediaEntry
	mediaBySHA  map[string]string
	legacyPaths map[string]string
	authors     map[string]bool
	terms       map[string]*parented
	posts       map[string]*parented
	comments    map[string]*parented
	menus       map[string]bool
	menuItems   map[string]*parented
	redirects   map[string]bool
	legacy      map[string]bool

	settings        settings.All
	settingsPresent map[string]bool
}

func newIndex() *index {
	return &index{
		users: map[string]string{}, media: map[string]*mediaEntry{}, mediaBySHA: map[string]string{},
		legacyPaths: map[string]string{}, authors: map[string]bool{}, terms: map[string]*parented{},
		posts: map[string]*parented{}, comments: map[string]*parented{}, menus: map[string]bool{},
		menuItems: map[string]*parented{}, redirects: map[string]bool{}, legacy: map[string]bool{},
	}
}

// canonicalMedia follows a duplicate-file alias to the record that is actually stored.
func (ix *index) canonicalMedia(key string) (string, bool) {
	e, ok := ix.media[key]
	if !ok {
		return "", false
	}
	if e.alias != "" {
		return e.alias, true
	}
	return key, true
}

type validator struct {
	dir string
	ix  *index
	rep *Report
}

func (v *validator) keyOK(key, kind string, seen map[string]bool) bool {
	prefix := v.ix.source + ":"
	if !strings.HasPrefix(key, prefix) || len(key) > 255 || strings.ContainsAny(key, " \t\n") {
		v.rep.errorf(key, "legacy_key", "must look like %s<kind>:<id>", prefix)
		return false
	}
	if seen != nil {
		if seen[key] {
			v.rep.errorf(key, "legacy_key", "duplicate %s legacy key", kind)
			return false
		}
		seen[key] = true
	}
	return true
}

func (v *validator) ref(owner, field string, key *string, known func(string) bool, what string) {
	if key != nil && !known(*key) {
		v.rep.errorf(owner, field, "references unknown %s %q", what, *key)
	}
}

func (v *validator) httpURL(owner, field, u string) {
	if u != "" && !(strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
		v.rep.errorf(owner, field, "must be an http(s) url")
	}
}

func (ix *index) hasMedia(k string) bool  { _, ok := ix.media[k]; return ok }
func (ix *index) hasUser(k string) bool   { _, ok := ix.users[k]; return ok }
func (ix *index) hasAuthor(k string) bool { return ix.authors[k] }
func (ix *index) hasTerm(k string) bool   { _, ok := ix.terms[k]; return ok }
func (ix *index) hasPost(k string) bool   { _, ok := ix.posts[k]; return ok }

// validate reads the whole bundle and fills the report. Nothing is written.
func validate(dir string, m *Manifest, allowedClasses, menuLocations map[string]bool, rep *Report) *index {
	ix := newIndex()
	ix.source, ix.generatedAt = m.Source, m.GeneratedAt
	ix.allowedClasses, ix.menuLocations = allowedClasses, menuLocations
	v := &validator{dir: dir, ix: ix, rep: rep}

	steps := []func() error{v.users, v.media, v.authors, v.terms, v.posts, v.comments, v.menus, v.redirects, v.legacyRecords, v.settings}
	for _, step := range steps {
		if err := step(); err != nil {
			rep.errorf("", "", "%v", err)
		}
	}
	v.depths("terms", ix.terms)
	v.depths("posts", ix.posts)
	v.depths("comments", ix.comments)
	v.depths("menu items", ix.menuItems)
	return ix
}

func checkManifest(m *Manifest, rep *Report) {
	if m.FormatVersion != FormatVersion {
		rep.errorf("", "manifest.format_version", "unsupported format version %d, want %d", m.FormatVersion, FormatVersion)
	}
	if !sourceRe.MatchString(m.Source) {
		rep.errorf("", "manifest.source", "must match %s", sourceRe)
	}
	if m.GeneratedAt.IsZero() {
		rep.errorf("", "manifest.generated_at", "required")
	}
}

func (v *validator) users() error {
	emails := map[string]string{}
	seen := map[string]bool{}
	return eachRecord(v.dir, fileUsers, func(_ int, r *UserRec) error {
		if !v.keyOK(r.LegacyKey, "user", seen) {
			return nil
		}
		email := strings.ToLower(strings.TrimSpace(r.Email))
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 {
			v.rep.errorf(r.LegacyKey, "email", "invalid email address")
		} else if other, dup := emails[email]; dup {
			v.rep.errorf(r.LegacyKey, "email", "same email as %s", other)
		}
		emails[email] = r.LegacyKey
		if strings.TrimSpace(r.DisplayName) == "" {
			v.rep.errorf(r.LegacyKey, "display_name", "required")
		}
		if !slices.Contains(roles, r.Role) {
			v.rep.errorf(r.LegacyKey, "role", "must be one of %s", strings.Join(roles, ", "))
		}
		v.ix.users[r.LegacyKey] = email
		return nil
	})
}

func (v *validator) media() error {
	seen := map[string]bool{}
	var fake int64
	return eachRecord(v.dir, fileMedia, func(_ int, r *MediaRec) error {
		if !v.keyOK(r.LegacyKey, "media", seen) {
			return nil
		}
		key := r.LegacyKey
		entry := &mediaEntry{sha: r.SHA256}
		if !shaRe.MatchString(r.SHA256) {
			v.rep.errorf(key, "sha256", "must be 64 lowercase hex characters")
		}
		if r.Filename == "" || strings.ContainsAny(r.Filename, `/\`) || len(r.Filename) > 255 {
			v.rep.errorf(key, "filename", "required, without path separators")
		}
		if !allowedMimes[r.Mime] {
			v.rep.errorf(key, "mime", "%q is not an allowed media type", r.Mime)
		}
		for f, p := range map[string]*int{"width": r.Width, "height": r.Height, "duration_seconds": r.DurationSeconds} {
			if p != nil && *p < 0 {
				v.rep.errorf(key, f, "must not be negative")
			}
		}
		v.checkFile(r)

		if canon, dup := v.ix.mediaBySHA[r.SHA256]; dup && r.SHA256 != "" {
			entry.alias = canon
			v.rep.warnf(key, "sha256", "same file as %s; references will point at that media item and this record's metadata is ignored", canon)
		} else {
			v.ix.mediaBySHA[r.SHA256] = key
			fake++
			entry.fake = fake
		}

		for i, p := range r.LegacyPaths {
			field := fmt.Sprintf("legacy_paths[%d]", i)
			if !strings.HasPrefix(p.Path, "/") || strings.Contains(p.Path, "..") || strings.ContainsAny(p.Path, "?# \t\n") {
				v.rep.errorf(key, field, "path must be an absolute url path without query, fragment or ..")
			}
			for _, pre := range reservedPrefixes {
				if strings.HasPrefix(p.Path, pre) {
					v.rep.errorf(key, field, "path is under the reserved prefix %s", pre)
				}
			}
			if owner, dup := v.ix.legacyPaths[p.Path]; dup {
				v.rep.errorf(key, field, "path %s is also claimed by %s", p.Path, owner)
			}
			v.ix.legacyPaths[p.Path] = key
			switch p.Variant {
			case "orig":
			case "resize":
				if p.Width == nil || p.Height == nil || *p.Width < 1 || *p.Height < 1 {
					v.rep.errorf(key, field, "resize variant needs positive width and height")
				}
			default:
				v.rep.errorf(key, field, "variant must be orig or resize")
			}
		}
		v.ix.media[key] = entry
		return nil
	})
}

func (v *validator) checkFile(r *MediaRec) {
	if !strings.HasPrefix(r.File, "files/") || !filepath.IsLocal(r.File) {
		v.rep.errorf(r.LegacyKey, "file", "must be a relative path under files/")
		return
	}
	f, err := os.Open(filepath.Join(v.dir, filepath.FromSlash(r.File)))
	if err != nil {
		v.rep.errorf(r.LegacyKey, "file", "cannot open: %v", errors.Unwrap(err))
		return
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	sniffed, _, _ := strings.Cut(http.DetectContentType(head[:n]), ";")
	if allowedMimes[sniffed] && sniffed != r.Mime {
		v.rep.errorf(r.LegacyKey, "mime", "declared %s but the file looks like %s", r.Mime, sniffed)
	}
	h := sha256.New()
	h.Write(head[:n])
	if _, err := io.Copy(h, f); err != nil {
		v.rep.errorf(r.LegacyKey, "file", "read failed: %v", err)
		return
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != r.SHA256 {
		v.rep.errorf(r.LegacyKey, "sha256", "file hash is %s", got)
	}
}

func (v *validator) seo(owner string, s *SEORec) {
	if s == nil {
		return
	}
	v.ref(owner, "seo.og_image_media", s.OGImageMedia, v.ix.hasMedia, "media")
	v.ref(owner, "seo.twitter_image_media", s.TwitterImageMedia, v.ix.hasMedia, "media")
	if s.Canonical != nil && *s.Canonical != "" && !sanitize.SafeURL(*s.Canonical) {
		v.rep.errorf(owner, "seo.canonical", "must be an http(s) or relative url")
	}
}

func (v *validator) authors() error {
	seen := map[string]bool{}
	slugs := map[string]string{}
	users := map[string]string{}
	return eachRecord(v.dir, fileAuthors, func(_ int, r *AuthorRec) error {
		if !v.keyOK(r.LegacyKey, "author", seen) {
			return nil
		}
		key := r.LegacyKey
		if !slugRe.MatchString(r.Slug) {
			v.rep.errorf(key, "slug", "must match %s", slugRe)
		} else if other, dup := slugs[r.Slug]; dup {
			v.rep.errorf(key, "slug", "same slug as %s", other)
		}
		slugs[r.Slug] = key
		if strings.TrimSpace(r.DisplayName) == "" {
			v.rep.errorf(key, "display_name", "required")
		}
		v.httpURL(key, "website", r.Website)
		for name, u := range r.Social {
			if !strings.HasPrefix(u, "https://") {
				v.rep.errorf(key, "social."+name, "must be an https url")
			}
		}
		v.ref(key, "avatar_media", r.AvatarMedia, v.ix.hasMedia, "media")
		v.ref(key, "user", r.User, v.ix.hasUser, "user")
		if r.User != nil {
			if other, dup := users[*r.User]; dup {
				v.rep.errorf(key, "user", "user %s is already linked to %s", *r.User, other)
			}
			users[*r.User] = key
		}
		v.seo(key, r.SEO)
		v.ix.authors[key] = true
		return nil
	})
}

func (v *validator) terms() error {
	seen := map[string]bool{}
	var recs []TermRec
	err := eachRecord(v.dir, fileTerms, func(_ int, r *TermRec) error {
		if !v.keyOK(r.LegacyKey, "term", seen) {
			return nil
		}
		key := r.LegacyKey
		if r.Taxonomy != "category" && r.Taxonomy != "tag" {
			v.rep.errorf(key, "taxonomy", "must be category or tag")
		}
		if !slugRe.MatchString(r.Slug) {
			v.rep.errorf(key, "slug", "must match %s", slugRe)
		}
		if strings.TrimSpace(r.Name) == "" {
			v.rep.errorf(key, "name", "required")
		}
		v.seo(key, r.SEO)
		v.ix.terms[key] = &parented{parent: deref(r.Parent), kind: r.Taxonomy}
		recs = append(recs, TermRec{LegacyKey: key, Taxonomy: r.Taxonomy, Slug: r.Slug, Parent: r.Parent})
		return nil
	})
	slugs := map[string]string{}
	for _, r := range recs {
		if p := deref(r.Parent); p != "" {
			parent, ok := v.ix.terms[p]
			switch {
			case !ok:
				v.rep.errorf(r.LegacyKey, "parent", "references unknown term %q", p)
			case r.Taxonomy != "category" || parent.kind != "category":
				v.rep.errorf(r.LegacyKey, "parent", "only categories can have a parent category")
			}
		}
		k := r.Taxonomy + "\x00" + deref(r.Parent) + "\x00" + r.Slug
		if other, dup := slugs[k]; dup {
			v.rep.errorf(r.LegacyKey, "slug", "same slug and parent as %s", other)
		}
		slugs[k] = r.LegacyKey
	}
	return err
}

// mediaRefs replaces legacy-key mediaId/posterMediaId values in doc with ids from resolve.
func mediaRefs(doc *content.Node, resolve func(string) (int64, bool)) []content.Issue {
	var issues []content.Issue
	var walk func(n *content.Node, path string)
	walk = func(n *content.Node, path string) {
		if n == nil {
			return
		}
		if n.Type == "image" || n.Type == "video" {
			for _, attr := range []string{"mediaId", "posterMediaId"} {
				val, ok := n.Attrs[attr]
				if !ok || val == nil {
					continue
				}
				p := strings.TrimPrefix(path+".attrs."+attr, ".")
				key, isStr := val.(string)
				if !isStr {
					issues = append(issues, content.Issue{Path: p, Message: "must be a media legacy key"})
					continue
				}
				id, found := resolve(key)
				if !found {
					issues = append(issues, content.Issue{Path: p, Message: fmt.Sprintf("references unknown media %q", key)})
					continue
				}
				n.Attrs[attr] = id
			}
		}
		for i, c := range n.Content {
			walk(c, strings.TrimPrefix(fmt.Sprintf("%s.content[%d]", path, i), "."))
		}
	}
	walk(doc, "")
	return issues
}

// prepareContent parses, resolves media keys and validates a post's content, normalizing it in place.
func prepareContent(raw json.RawMessage, allowed map[string]bool, resolve func(string) (int64, bool)) (*content.Node, []content.Issue, content.Result, error) {
	doc, err := content.Parse(raw)
	if err != nil {
		return nil, nil, content.Result{}, err
	}
	refIssues := mediaRefs(doc, resolve)
	res := content.Validate(doc, content.Options{AllowedClasses: allowed, DropUnknownClasses: true, AllowRawHTML: true})
	return doc, refIssues, res, nil
}

func (v *validator) posts() error {
	seen := map[string]bool{}
	type slugInfo struct{ key, typ, parent, slug string }
	var slugs []slugInfo
	resolve := func(key string) (int64, bool) {
		canon, ok := v.ix.canonicalMedia(key)
		if !ok {
			return 0, false
		}
		return v.ix.media[canon].fake, true
	}

	err := eachRecord(v.dir, filePosts, func(_ int, r *PostRec) error {
		if !v.keyOK(r.LegacyKey, "post", seen) {
			return nil
		}
		key := r.LegacyKey
		if r.Type != "post" && r.Type != "page" {
			v.rep.errorf(key, "type", "must be post or page")
		}
		if !slices.Contains(postStatuses, r.Status) {
			v.rep.errorf(key, "status", "must be one of %s", strings.Join(postStatuses, ", "))
		}
		if !slugRe.MatchString(r.Slug) {
			v.rep.errorf(key, "slug", "must match %s", slugRe)
		}
		if r.Parent == nil && (r.Slug == "_plugins" || r.Slug == "_theme") {
			v.rep.warnf(key, "slug", "/%s/ is reserved for theme and plugin files; it may be unreachable", r.Slug)
		}
		if r.CreatedAt == nil {
			v.rep.errorf(key, "created_at", "required")
		}
		if (r.Status == "published" || r.Status == "scheduled") && r.PublishedAt == nil {
			v.rep.errorf(key, "published_at", "required for %s posts", r.Status)
		}
		if r.CommentStatus != "" && r.CommentStatus != "open" && r.CommentStatus != "closed" {
			v.rep.errorf(key, "comment_status", "must be open or closed")
		}
		if r.Template != nil && !templateRe.MatchString(*r.Template) {
			v.rep.errorf(key, "template", "must match %s", templateRe)
		}
		if r.Parent != nil && r.Type != "page" {
			v.rep.errorf(key, "parent", "only pages can have a parent")
		}
		v.ref(key, "featured_media", r.FeaturedMedia, v.ix.hasMedia, "media")

		authors := map[string]bool{}
		for i, a := range r.Authors {
			v.ref(key, fmt.Sprintf("authors[%d]", i), &a, v.ix.hasAuthor, "author")
			if authors[a] {
				v.rep.errorf(key, fmt.Sprintf("authors[%d]", i), "duplicate author %q", a)
			}
			authors[a] = true
		}
		if len(r.Authors) == 0 && r.Type == "post" {
			v.rep.warnf(key, "authors", "post has no authors")
		}
		primaries := 0
		terms := map[string]bool{}
		for i, t := range r.Terms {
			field := fmt.Sprintf("terms[%d]", i)
			info, ok := v.ix.terms[t.Term]
			if !ok {
				v.rep.errorf(key, field, "references unknown term %q", t.Term)
				continue
			}
			if terms[t.Term] {
				v.rep.errorf(key, field, "duplicate term %q", t.Term)
			}
			terms[t.Term] = true
			if t.Primary {
				primaries++
				if info.kind != "category" {
					v.rep.errorf(key, field, "the primary term must be a category")
				}
			}
		}
		if primaries > 1 {
			v.rep.errorf(key, "terms", "at most one primary category")
		}
		v.seo(key, r.SEO)

		urls := map[string]bool{}
		for i, vid := range r.Videos {
			field := fmt.Sprintf("videos[%d]", i)
			if !strings.HasPrefix(vid.URL, "https://") && !strings.HasPrefix(vid.URL, "http://") {
				v.rep.errorf(key, field+".url", "must be an http(s) url")
			}
			if urls[vid.URL] {
				v.rep.errorf(key, field+".url", "duplicate video url")
			}
			urls[vid.URL] = true
			v.httpURL(key, field+".thumbnail_url", vid.ThumbnailURL)
			v.ref(key, field+".thumbnail_media", vid.ThumbnailMedia, v.ix.hasMedia, "media")
		}

		if len(r.Content) == 0 {
			v.rep.errorf(key, "content", "required")
		} else {
			_, refIssues, res, err := prepareContent(r.Content, v.ix.allowedClasses, resolve)
			if err != nil {
				v.rep.errorf(key, "content", "%v", err)
			}
			for _, is := range append(refIssues, res.Errors...) {
				v.rep.errorf(key, "content."+is.Path, "%s", is.Message)
			}
			for _, is := range res.Warnings {
				v.rep.warnf(key, "content."+is.Path, "%s", is.Message)
			}
			for _, rm := range res.Removals {
				v.rep.Removals = append(v.rep.Removals, SanitizerRemoval{LegacyKey: key, Path: rm.Path, Removal: rm.Removal})
			}
		}

		v.ix.posts[key] = &parented{parent: deref(r.Parent), kind: r.Type}
		slugs = append(slugs, slugInfo{key, r.Type, deref(r.Parent), r.Slug})
		return nil
	})

	unique := map[string]string{}
	for _, s := range slugs {
		if s.parent != "" {
			if p, ok := v.ix.posts[s.parent]; !ok || p.kind != "page" {
				v.rep.errorf(s.key, "parent", "references unknown page %q", s.parent)
			}
		}
		k := s.typ + "\x00" + s.parent + "\x00" + s.slug
		if other, dup := unique[k]; dup {
			v.rep.errorf(s.key, "slug", "same slug and parent as %s", other)
		}
		unique[k] = s.key
	}
	return err
}

func (v *validator) comments() error {
	seen := map[string]bool{}
	err := eachRecord(v.dir, fileComments, func(_ int, r *CommentRec) error {
		if !v.keyOK(r.LegacyKey, "comment", seen) {
			return nil
		}
		key := r.LegacyKey
		if !v.ix.hasPost(r.Post) {
			v.rep.errorf(key, "post", "references unknown post %q", r.Post)
		}
		if r.AuthorEmail != "" {
			if _, err := mail.ParseAddress(r.AuthorEmail); err != nil {
				v.rep.warnf(key, "author_email", "invalid email address; stored as given")
			}
		}
		v.httpURL(key, "author_url", r.AuthorURL)
		v.ref(key, "user", r.User, v.ix.hasUser, "user")
		if r.IP != "" {
			if _, err := netip.ParseAddr(r.IP); err != nil {
				v.rep.errorf(key, "ip", "invalid ip address")
			}
		}
		if strings.TrimSpace(r.Body) == "" {
			v.rep.errorf(key, "body", "required")
		}
		if !slices.Contains(commentStatuses, r.Status) {
			v.rep.errorf(key, "status", "must be one of %s", strings.Join(commentStatuses, ", "))
		}
		if r.CreatedAt == nil {
			v.rep.errorf(key, "created_at", "required")
		}
		v.ix.comments[key] = &parented{parent: deref(r.Parent), kind: r.Post}
		return nil
	})
	for key, c := range v.ix.comments {
		if c.parent == "" {
			continue
		}
		if p, ok := v.ix.comments[c.parent]; !ok {
			v.rep.errorf(key, "parent", "references unknown comment %q", c.parent)
		} else if p.kind != c.kind {
			v.rep.errorf(key, "parent", "parent comment belongs to a different post")
		}
	}
	return err
}

func (v *validator) menus() error {
	seen, itemSeen := map[string]bool{}, map[string]bool{}
	slugs, locations := map[string]string{}, map[string]string{}
	return eachRecord(v.dir, fileMenus, func(_ int, r *MenuRec) error {
		if !v.keyOK(r.LegacyKey, "menu", seen) {
			return nil
		}
		key := r.LegacyKey
		if !menuSlugRe.MatchString(r.Slug) {
			v.rep.errorf(key, "slug", "must match %s", menuSlugRe)
		} else if other, dup := slugs[r.Slug]; dup {
			v.rep.errorf(key, "slug", "same slug as %s", other)
		}
		slugs[r.Slug] = key
		if strings.TrimSpace(r.Name) == "" {
			v.rep.errorf(key, "name", "required")
		}
		if loc := deref(r.Location); loc != "" {
			if other, dup := locations[loc]; dup {
				v.rep.errorf(key, "location", "location %q is also assigned to %s", loc, other)
			}
			locations[loc] = key
			if v.ix.menuLocations != nil && !v.ix.menuLocations[loc] {
				v.rep.warnf(key, "location", "the theme does not declare menu location %q", loc)
			}
		}
		v.ix.menus[key] = true

		for i, it := range r.Items {
			field := fmt.Sprintf("items[%d]", i)
			if !v.keyOK(it.LegacyKey, "menu item", itemSeen) {
				continue
			}
			if !slices.Contains(menuKinds, it.Kind) {
				v.rep.errorf(it.LegacyKey, field+".kind", "must be one of %s", strings.Join(menuKinds, ", "))
			}
			switch it.Kind {
			case "url":
				if it.URL == nil || !sanitize.SafeURL(*it.URL) || *it.URL == "" {
					v.rep.errorf(it.LegacyKey, field+".url", "url items need an http(s), mailto, tel or relative url")
				}
			case "post":
				v.requireRef(it.LegacyKey, field+".target", it.Target, v.ix.hasPost, "post")
			case "term":
				v.requireRef(it.LegacyKey, field+".target", it.Target, v.ix.hasTerm, "term")
			case "author":
				v.requireRef(it.LegacyKey, field+".target", it.Target, v.ix.hasAuthor, "author")
			}
			for _, c := range it.CSSClasses {
				if !classRe.MatchString(c) {
					v.rep.errorf(it.LegacyKey, field+".css_classes", "invalid class %q", c)
				}
			}
			v.ix.menuItems[it.LegacyKey] = &parented{parent: deref(it.Parent), kind: key}
		}
		for i, it := range r.Items {
			if p := deref(it.Parent); p != "" {
				if parent, ok := v.ix.menuItems[p]; !ok || parent.kind != key {
					v.rep.errorf(it.LegacyKey, fmt.Sprintf("items[%d].parent", i), "parent must be an item of the same menu")
				}
			}
		}
		return nil
	})
}

func (v *validator) requireRef(owner, field string, key *string, known func(string) bool, what string) {
	if key == nil {
		v.rep.errorf(owner, field, "required for %s items", what)
		return
	}
	v.ref(owner, field, key, known, what)
}

func (v *validator) redirects() error {
	seen := map[string]bool{}
	exact := map[string]string{}
	return eachRecord(v.dir, fileRedirects, func(_ int, r *RedirectRec) error {
		if !v.keyOK(r.LegacyKey, "redirect", seen) {
			return nil
		}
		key := r.LegacyKey
		switch r.Match {
		case "exact", "prefix":
			if !strings.HasPrefix(r.Source, "/") {
				v.rep.errorf(key, "source", "must start with /")
			}
			if r.Match == "exact" {
				if other, dup := exact[r.Source]; dup {
					v.rep.errorf(key, "source", "same exact source as %s", other)
				}
				exact[r.Source] = key
			}
		case "regex":
			if _, err := regexp.Compile(r.Source); err != nil {
				v.rep.errorf(key, "source", "not a valid RE2 regular expression: %v", err)
			}
		default:
			v.rep.errorf(key, "match", "must be exact, prefix or regex")
		}
		if strings.HasPrefix(r.Source, "/admin") || strings.HasPrefix(r.Source, "^/admin") {
			v.rep.errorf(key, "source", "/admin is reserved")
		}
		if !slices.Contains(redirectStatuses, r.Status) {
			v.rep.errorf(key, "status", "must be one of 301, 302, 307, 308, 410, 451")
		}
		if r.Status != 410 && r.Status != 451 && (r.Target == nil || *r.Target == "" || !sanitize.SafeURL(*r.Target)) {
			v.rep.errorf(key, "target", "required: an http(s) or relative url")
		}
		v.ix.redirects[key] = true
		return nil
	})
}

func (v *validator) legacyRecords() error {
	seen := map[string]bool{}
	return eachRecord(v.dir, fileLegacy, func(_ int, r *LegacyRec) error {
		if !v.keyOK(r.LegacyKey, "legacy record", seen) {
			return nil
		}
		if strings.TrimSpace(r.Kind) == "" {
			v.rep.errorf(r.LegacyKey, "kind", "required")
		}
		if len(r.Source) == 0 || r.Source[0] != '{' {
			v.rep.errorf(r.LegacyKey, "source", "must be a json object")
		}
		v.ix.legacy[r.LegacyKey] = true
		return nil
	})
}

func (v *validator) settings() error {
	b, err := os.ReadFile(filepath.Join(v.dir, "settings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		v.ix.settings = settings.Defaults()
		v.ix.settingsPresent = map[string]bool{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("settings.json: %w", err)
	}
	all, present, err := settings.Decode(b)
	if err != nil {
		v.rep.errorf("", "settings", "%v", err)
		return nil
	}
	for name, ref := range all.Refs() {
		if *ref == nil {
			continue
		}
		r := **ref
		switch {
		case r.ID != 0:
			v.rep.errorf("", "settings."+name, "bundles must reference by legacy key")
		case settings.RefKind[name] == "media" && !v.ix.hasMedia(r.LegacyKey):
			v.rep.errorf("", "settings."+name, "references unknown media %q", r.LegacyKey)
		case settings.RefKind[name] == "post":
			if p, ok := v.ix.posts[r.LegacyKey]; !ok || p.kind != "page" {
				v.rep.errorf("", "settings."+name, "references unknown page %q", r.LegacyKey)
			}
		}
	}
	v.ix.settings, v.ix.settingsPresent = all, present
	return nil
}

// depths sets each entry's distance from its root and reports parent cycles.
func (v *validator) depths(what string, entries map[string]*parented) {
	const unset = -1
	for _, e := range entries {
		e.depth = unset
	}
	for key := range entries {
		var chain []*parented
		cur, k := entries[key], key
		visiting := map[string]bool{}
		for cur != nil && cur.depth == unset {
			if visiting[k] {
				v.rep.errorf(key, "parent", "%s parent chain has a cycle", what)
				for _, c := range chain {
					c.depth = 0
				}
				chain = nil
				break
			}
			visiting[k] = true
			chain = append(chain, cur)
			k = cur.parent
			cur = entries[k]
		}
		base := -1
		if cur != nil {
			base = cur.depth
		}
		for i := len(chain) - 1; i >= 0; i-- {
			base++
			chain[i].depth = base
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
