// Package taxonomy is the admin side of terms, authors and menus: validation, writes, auto redirects and purges.
package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/audit"
	"afsc/internal/redirects"
	"afsc/internal/site"
	"afsc/internal/theme"
)

type Service struct {
	DB        *pgxpool.Pool
	Site      *site.Site
	Redirects *redirects.Store
	ThemeDir  string
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

func badField(field, format string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Field: field, Message: fmt.Sprintf(format, args...)}
}

func conflict(field, format string, args ...any) *Error {
	return &Error{Status: http.StatusConflict, Field: field, Message: fmt.Sprintf(format, args...)}
}

func notFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Message: what + " not found"}
}

// Actor is who makes a change, for the audit log and redirect authorship.
type Actor struct {
	User uuid.NullUUID
	IP   string
}

func (act Actor) ref() *string {
	if !act.User.Valid {
		return nil
	}
	s := act.User.UUID.String()
	return &s
}

func (act Actor) audit(ctx context.Context, tx pgx.Tx, action, entity string, id int64, diff any) error {
	return audit.Write(ctx, tx, audit.Entry{Actor: act.User, Action: action, EntityType: entity,
		EntityID: strconv.FormatInt(id, 10), Diff: diff, IP: act.IP})
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Slugify lowercases s and joins its runs of letters, digits and underscores with single dashes.
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := b.String()
	if utf8.RuneCountInString(out) > 190 {
		out = string([]rune(out)[:190])
	}
	return strings.Trim(out, "-")
}

// pickSlug decides the slug to store. An unchanged slug is kept verbatim (imported slugs may not be in Slugify's
// form); an explicit one must be free; one derived from the name gets -2, -3… until it is.
func pickSlug(explicit, current, name string, taken func(string) (bool, error)) (string, error) {
	if explicit != "" && explicit == current {
		return current, nil
	}
	if explicit == "" && current != "" {
		return current, nil
	}
	base := Slugify(explicit)
	if explicit == "" {
		base = Slugify(name)
	}
	if base == "" {
		if explicit != "" {
			return "", badField("slug", "use letters or digits in the slug")
		}
		base = "item"
	}
	for i := 1; i <= 100; i++ {
		s := base
		if i > 1 {
			s = base + "-" + strconv.Itoa(i)
		}
		used, err := taken(s)
		if err != nil {
			return "", err
		}
		if !used {
			return s, nil
		}
		if explicit != "" {
			return "", conflict("slug", "the slug %q is already used", s)
		}
	}
	return "", conflict("slug", "pick a different slug")
}

func checkLen(field, v string, lo, hi int) error {
	n := utf8.RuneCountInString(strings.TrimSpace(v))
	if n < lo {
		return badField(field, "is required")
	}
	if n > hi {
		return badField(field, "must be at most %d characters", hi)
	}
	return nil
}

// httpURL accepts an absolute http(s) URL.
func httpURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && len(raw) <= 2000
}

func (s *Service) perm() (*site.Permalinks, error) {
	return site.NewPermalinks(s.Site.Settings().Permalinks, time.UTC)
}

func (s *Service) manifest() (*theme.Manifest, error) {
	return theme.LoadManifest(s.ThemeDir)
}

// refresh reloads the site snapshot on every replica, so URL sets taken next reflect the committed change.
func (s *Service) refresh(ctx context.Context) error {
	return s.Site.Invalidate(ctx, site.Event{Refresh: true})
}

// redirect records 301s for URLs that moved between before and after and returns the purge for the rules it touched.
func (s *Service) redirect(ctx context.Context, before, after map[string]string, act Actor) (site.Event, error) {
	moves := redirects.Moves(before, after)
	if len(moves) == 0 {
		return site.Event{}, nil
	}
	res, err := s.Redirects.SlugChange(ctx, moves, act.ref())
	if err != nil {
		return site.Event{}, err
	}
	return site.Event{Refresh: res.Purge.Refresh, All: res.Purge.Refresh, Paths: res.Purge.Paths}, nil
}

// urlsNow takes the current URL of every key in before ("term:3", "post:12", "author:5"); keys that no longer
// have a public URL are left out.
func (s *Service) urlsNow(ctx context.Context, before map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for key := range before {
		if _, done := out[key]; done {
			continue
		}
		kind, idStr, _ := strings.Cut(key, ":")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		var m map[string]string
		switch kind {
		case "term":
			m, err = s.Site.TermURLs(ctx, id)
		case "post":
			m, err = s.Site.PostURLs(ctx, id)
		case "author":
			m = s.Site.AuthorURLs(id)
		}
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			if _, want := before[k]; want {
				out[k] = v
			}
		}
	}
	return out, nil
}

// abs makes a public path absolute.
func (s *Service) abs(path string) *string {
	u := s.Site.BaseURL() + path
	return &u
}
