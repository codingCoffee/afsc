package main

import (
	"errors"
	"html"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"afsc/internal/comments"
	"afsc/internal/site"
)

const (
	commentBodyLimit = 32 << 10
	noticeCookieTTL  = 10 * time.Minute
)

// postComment is POST /comments, the only public write endpoint (§11). It never touches the page cache: the
// response is a 303 back to the post, plus a short-lived afsc_comment cookie that makes the visitor's next page
// views bypass the cache so they see their comment awaiting moderation (Q5).
func postComment(svc *comments.Service, pub *site.Site, baseURL string) echo.HandlerFunc {
	return func(c *echo.Context) error {
		r := c.Request()
		r.Body = http.MaxBytesReader(nil, r.Body, commentBodyLimit)
		if err := r.ParseForm(); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return commentError(c, baseURL, http.StatusRequestEntityTooLarge, "Your comment is too long.")
			}
			return commentError(c, baseURL, http.StatusBadRequest, "The comment form could not be read.")
		}
		f := r.PostForm
		postID, _ := strconv.ParseInt(f.Get("post_id"), 10, 64)
		parentID, _ := strconv.ParseInt(f.Get("parent_id"), 10, 64)
		res, err := svc.Create(r.Context(), comments.Input{
			PostID: postID, ParentID: parentID, Token: f.Get("token"),
			Name: f.Get("author_name"), Email: f.Get("author_email"), URL: f.Get("author_url"), Body: f.Get("body"),
			Honeypot: f.Get(site.HoneypotField), IP: clientIP(c), UserAgent: r.UserAgent(),
		})
		var verr *comments.ValidationError
		switch {
		case errors.As(err, &verr):
			keys := make([]string, 0, len(verr.Fields))
			for k := range verr.Fields {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			msgs := make([]string, 0, len(keys))
			for _, k := range keys {
				msgs = append(msgs, verr.Fields[k])
			}
			return commentError(c, baseURL, http.StatusBadRequest, msgs...)
		case errors.Is(err, comments.ErrBadToken):
			return commentError(c, baseURL, http.StatusBadRequest,
				"The comment form has expired or was changed. Reload the page and try again.")
		case errors.Is(err, comments.ErrClosed):
			return commentError(c, baseURL, http.StatusForbidden, "Comments are closed on this page.")
		case errors.Is(err, comments.ErrNotFound):
			return commentError(c, baseURL, http.StatusNotFound, "This page doesn't take comments.")
		case errors.Is(err, comments.ErrRateLimited):
			c.Response().Header().Set("Retry-After", "600")
			return commentError(c, baseURL, http.StatusTooManyRequests,
				"You're commenting too quickly. Wait a few minutes and try again.")
		case err != nil:
			log.Printf("comments: create: %v", err)
			return commentError(c, baseURL, http.StatusInternalServerError, "Your comment couldn't be saved. Try again later.")
		}

		loc, err := pub.CommentLocation(r.Context(), res.PostID, res.ID, res.Status)
		if err != nil || loc == "" {
			log.Printf("comments: location for #%d: %v", res.ID, err)
			loc = baseURL + "/"
		}
		// spam gets the same answer as pending, so a bot learns nothing from the response
		if res.Status != "approved" {
			c.SetCookie(&http.Cookie{
				Name: site.NoticeCookie, Value: pub.CommentSigner().NoticeCookie(res.ID, time.Now().Add(noticeCookieTTL)),
				Path: "/", MaxAge: int(noticeCookieTTL.Seconds()), HttpOnly: true,
				Secure: strings.HasPrefix(baseURL, "https://"), SameSite: http.SameSiteLaxMode,
			})
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.Redirect(http.StatusSeeOther, loc)
	}
}

// commentError is the no-JS error page: unthemed, listing what went wrong, with a link back (Q16).
func commentError(c *echo.Context, baseURL string, status int, msgs ...string) error {
	back := baseURL + "/"
	if ref := c.Request().Referer(); strings.HasPrefix(ref, baseURL+"/") {
		back = ref
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<meta name="robots" content="noindex"><title>Comment not posted</title>`)
	b.WriteString(`<main style="max-width:36rem;margin:3rem auto;padding:0 1rem;font-family:system-ui,sans-serif;line-height:1.5">`)
	b.WriteString(`<h1 style="font-size:1.25rem">Your comment wasn't posted</h1><ul>`)
	for _, m := range msgs {
		b.WriteString("<li>" + html.EscapeString(m) + "</li>")
	}
	b.WriteString(`</ul><p><a href="` + html.EscapeString(back) + `">Go back</a></p></main></html>`)
	h := c.Response().Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	return c.HTML(status, b.String())
}
