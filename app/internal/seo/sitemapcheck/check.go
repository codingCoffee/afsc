// Package sitemapcheck checks sitemap XML structurally against the vendored sitemaps.org, image and video XSDs
// (testdata/xsd), for the elements AFSC emits. Go has no XSD validator; scripts/check-sitemaps.sh runs xmllint.
package sitemapcheck

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"afsc/internal/seo"
)

type elem struct {
	name xml.Name
	text string
	kids []*elem
}

func parse(b []byte) (*elem, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var stack []*elem
	var root *elem
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			e := &elem{name: t.Name}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, e)
			} else if root != nil {
				return nil, errors.New("more than one root element")
			} else {
				root = e
			}
			stack = append(stack, e)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("no root element")
	}
	return root, nil
}

// field is one entry of an xsd:sequence. check validates the element's text.
type field struct {
	ns, name string
	min, max int
	check    func(string) error
}

// sequence checks e's children against fields in order; trailing children matching rest are passed to it.
func sequence(e *elem, fields []field, rest func(*elem) error) error {
	i := 0
	for _, f := range fields {
		n := 0
		for i < len(e.kids) && e.kids[i].name.Space == f.ns && e.kids[i].name.Local == f.name {
			k := e.kids[i]
			if len(k.kids) > 0 {
				return fmt.Errorf("%s/%s: unexpected child elements", e.name.Local, f.name)
			}
			if f.check != nil {
				if err := f.check(strings.TrimSpace(k.text)); err != nil {
					return fmt.Errorf("%s/%s: %w", e.name.Local, f.name, err)
				}
			}
			n++
			i++
		}
		if n < f.min || (f.max > 0 && n > f.max) {
			return fmt.Errorf("%s: %d <%s>, want %d to %d", e.name.Local, n, f.name, f.min, f.max)
		}
	}
	for ; i < len(e.kids); i++ {
		if rest == nil {
			return fmt.Errorf("%s: unexpected <%s %s>", e.name.Local, e.kids[i].name.Space, e.kids[i].name.Local)
		}
		if err := rest(e.kids[i]); err != nil {
			return err
		}
	}
	return nil
}

func uri(min, max int) func(string) error {
	return func(s string) error {
		u, err := url.Parse(s)
		switch {
		case err != nil:
			return err
		case len(s) < min || len(s) > max:
			return fmt.Errorf("%q: length %d outside %d-%d", s, len(s), min, max)
		case (u.Scheme != "https" && u.Scheme != "http") || u.Host == "":
			return fmt.Errorf("%q is not an absolute http(s) URL", s)
		}
		return nil
	}
}

func dateTime(s string) error {
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return nil
	}
	_, err := time.Parse(time.DateOnly, s)
	return err
}

func maxRunes(n int) func(string) error {
	return func(s string) error {
		if c := utf8.RuneCountInString(s); c > n || c == 0 {
			return fmt.Errorf("%d characters, want 1 to %d", c, n)
		}
		return nil
	}
}

func duration(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 28800 {
		return fmt.Errorf("%q: want an integer 0-28800", s)
	}
	return nil
}

func yesNo(s string) error {
	switch s {
	case "yes", "no", "Yes", "No", "YES", "NO":
		return nil
	}
	return fmt.Errorf("%q: want yes or no", s)
}

const sm = seo.NSSitemap

var imageSeq = []field{{seo.NSImage, "loc", 1, 1, uri(1, 2048)}}

var videoSeq = []field{
	{seo.NSVideo, "thumbnail_loc", 1, 1, uri(1, 2048)},
	{seo.NSVideo, "title", 1, 1, maxRunes(100)},
	{seo.NSVideo, "description", 1, 1, maxRunes(2048)},
	{seo.NSVideo, "content_loc", 0, 1, uri(1, 2048)},
	{seo.NSVideo, "player_loc", 0, 1, uri(1, 2048)},
	{seo.NSVideo, "duration", 0, 1, duration},
	{seo.NSVideo, "publication_date", 0, 1, dateTime},
	{seo.NSVideo, "family_friendly", 0, 1, yesNo},
}

var urlSeq = []field{
	{sm, "loc", 1, 1, uri(12, 2048)},
	{sm, "lastmod", 0, 1, dateTime},
}

// URLSet checks a <urlset> document.
func URLSet(b []byte) error {
	root, err := parse(b)
	if err != nil {
		return err
	}
	if root.name != (xml.Name{Space: sm, Local: "urlset"}) {
		return fmt.Errorf("root is %v, want urlset", root.name)
	}
	if n := len(root.kids); n < 1 || n > seo.MaxSitemapURLs {
		return fmt.Errorf("%d <url> elements, want 1 to %d", n, seo.MaxSitemapURLs)
	}
	for _, u := range root.kids {
		if u.name != (xml.Name{Space: sm, Local: "url"}) {
			return fmt.Errorf("urlset: unexpected <%s>", u.name.Local)
		}
		images := 0
		err := sequence(u, urlSeq, func(k *elem) error {
			switch k.name {
			case xml.Name{Space: seo.NSImage, Local: "image"}:
				if images++; images > 1000 {
					return errors.New("url: more than 1000 images")
				}
				return sequence(k, imageSeq, nil)
			case xml.Name{Space: seo.NSVideo, Local: "video"}:
				if err := sequence(k, videoSeq, nil); err != nil {
					return err
				}
				if !has(k, "content_loc") && !has(k, "player_loc") {
					return errors.New("video: needs content_loc or player_loc")
				}
				return nil
			}
			return fmt.Errorf("url: unexpected <%s %s>", k.name.Space, k.name.Local)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func has(e *elem, local string) bool {
	for _, k := range e.kids {
		if k.name.Local == local {
			return true
		}
	}
	return false
}

// Index checks a <sitemapindex> document.
func Index(b []byte) error {
	root, err := parse(b)
	if err != nil {
		return err
	}
	if root.name != (xml.Name{Space: sm, Local: "sitemapindex"}) {
		return fmt.Errorf("root is %v, want sitemapindex", root.name)
	}
	if n := len(root.kids); n < 1 || n > seo.MaxSitemapURLs {
		return fmt.Errorf("%d <sitemap> elements, want 1 to %d", n, seo.MaxSitemapURLs)
	}
	for _, s := range root.kids {
		if s.name != (xml.Name{Space: sm, Local: "sitemap"}) {
			return fmt.Errorf("sitemapindex: unexpected <%s>", s.name.Local)
		}
		if err := sequence(s, urlSeq, nil); err != nil {
			return err
		}
	}
	return nil
}
