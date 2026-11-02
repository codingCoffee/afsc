package transform

import (
	"crypto/sha1"
	"encoding/hex"
	"net/mail"
	"sort"
	"strconv"
	"strings"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/phpser"
	"afsc/migrators/afsc-wp/internal/records"
)

var roleRank = map[string]int{"administrator": 4, "editor": 3, "author": 2, "contributor": 1}
var roleMap = map[string]string{"administrator": "admin", "editor": "editor", "author": "author", "contributor": "contributor"}

// importable reports whether a post of this type and status becomes a bundle post (§5.2).
func (s *state) importable(p *extract.Post) bool {
	return s.postType(p) != "" && mapStatus(p) != ""
}

func (s *state) postType(p *extract.Post) string {
	switch p.Type {
	case "post", "page":
		return p.Type
	case "attachment", "revision", "nav_menu_item":
		return ""
	}
	switch s.decide("post_type", p.Type, "skip") {
	case "post":
		return "post"
	case "page":
		return "page"
	}
	return ""
}

func mapStatus(p *extract.Post) string {
	st := map[string]string{"publish": "published", "future": "scheduled", "draft": "draft", "pending": "pending", "private": "private"}[p.Status]
	if st != "" && p.Password != "" {
		return "private"
	}
	return st
}

func role(u *extract.User, prefix string) string {
	v, _ := phpser.Decode(u.Meta.First(prefix + "capabilities"))
	best, rank := "", 0
	if m, ok := v.(map[string]any); ok {
		for r, on := range m {
			if b, _ := on.(bool); b && roleRank[r] > rank {
				best, rank = r, roleRank[r]
			}
		}
	}
	return best
}

func (s *state) people() ([]*records.User, []*records.Author) {
	authored := map[int64]bool{}
	for _, p := range s.ds.Posts {
		if s.importable(p) {
			authored[p.Author] = true
		}
	}
	var users []*records.User
	var authors []*records.Author
	emails := map[string]string{}
	slugs := map[string]bool{}
	for _, u := range s.ds.Users {
		r := role(u, s.ds.Prefix)
		key := s.key("user", u.ID)
		if r == "" && !authored[u.ID] {
			s.rep.skip("users", "subscriber or no role, no posts", key)
			continue
		}
		if r == "" {
			r = "contributor"
			s.rep.note("%s has posts but no contributor-or-higher role; imported as contributor", key)
		}
		email := strings.ToLower(strings.TrimSpace(u.Email))
		a, err := mail.ParseAddress(email)
		switch {
		case err != nil || a.Address != email:
			s.rep.skip("users", "invalid email (author kept without a user)", key)
		case emails[email] != "":
			s.rep.skip("users", "duplicate email (author kept without a user)", key)
		default:
			emails[email] = key
			name := strings.TrimSpace(u.DisplayName)
			if name == "" {
				name = u.Login
			}
			users = append(users, &records.User{LegacyKey: key, Email: email, DisplayName: name, Role: roleMap[r]})
			s.userKey[u.ID] = key
		}
		if !authored[u.ID] {
			continue
		}
		au := s.author(u, key, slugs)
		authors = append(authors, au)
		s.authorKey[u.ID] = key
	}
	if s.set.On("guest_author") {
		authors = append(authors, s.guests(slugs)...)
	}
	sort.Slice(authors, func(i, j int) bool { return authors[i].LegacyKey < authors[j].LegacyKey })
	return users, authors
}

var socialKeys = map[string]string{"facebook": "facebook", "instagram": "instagram", "linkedin": "linkedin", "myspace": "myspace",
	"pinterest": "pinterest", "soundcloud": "soundcloud", "tumblr": "tumblr", "youtube": "youtube", "wikipedia": "wikipedia", "twitter": "x"}

func (s *state) author(u *extract.User, key string, slugs map[string]bool) *records.Author {
	slug := extract.DecodeSlug(u.Nicename)
	if !validSlug(slug) {
		fixed := Slugify(slug)
		if fixed == "" {
			fixed = "author-" + strconv.FormatInt(u.ID, 10)
		}
		s.rep.note("%s: author slug %q changed to %q; /author/ URL changes", key, slug, fixed)
		s.pathMap["/author/"+slug+"/"] = "/author/" + fixed + "/"
		slug = fixed
	}
	slug = uniq(slug, slugs)
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = u.Login
	}
	a := &records.Author{LegacyKey: key, Slug: slug, DisplayName: name, Bio: strings.TrimSpace(u.Meta.First(s.set.Key("core", "user_bio")))}
	if w := strings.TrimSpace(u.URL); strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
		a.Website = w
	}
	if _, ok := s.userKey[u.ID]; ok {
		a.User = records.Ptr(key)
	}
	if s.set.On("user_profile_picture") {
		if id := u.Meta.First(s.set.Key("user_profile_picture", "avatar")); id != "" {
			if k := s.resolveImage(id, ""); k != nil {
				a.AvatarMedia = k
			} else {
				s.rep.note("%s: avatar attachment %s not found", key, id)
			}
		}
	}
	if s.set.On("yoast") {
		social := map[string]string{}
		for _, k := range s.set.List("yoast", "author_social") {
			v := strings.TrimSpace(u.Meta.First(k))
			if v == "" {
				continue
			}
			if k == "twitter" && !strings.Contains(v, "/") {
				v = "https://x.com/" + strings.TrimPrefix(v, "@")
			}
			name := socialKeys[k]
			if name == "" {
				name = k
			}
			if strings.HasPrefix(v, "https://") {
				social[name] = v
			}
		}
		if len(social) > 0 {
			a.Social = social
		}
		seo := &records.SEO{}
		if t := strings.TrimSpace(u.Meta.First(s.set.Key("yoast", "author_title"))); t != "" {
			seo.Title = records.Ptr(t)
		}
		if d := strings.TrimSpace(u.Meta.First(s.set.Key("yoast", "author_description"))); d != "" {
			seo.Description = records.Ptr(d)
		}
		if u.Meta.First(s.set.Key("yoast", "author_noindex")) == "on" {
			seo.RobotsNoindex = records.Ptr(true)
		}
		if !seo.Empty() {
			a.SEO = seo
		}
	}
	return a
}

// guests creates one author per unique guest identity (normalized name plus email or URL).
func (s *state) guests(slugs map[string]bool) []*records.Author {
	k := func(f string) string { return s.set.Key("guest_author", f) }
	var out []*records.Author
	seen := map[string]bool{}
	for _, p := range s.ds.Posts {
		if !s.importable(p) {
			continue
		}
		name := strings.TrimSpace(p.Meta.First(k("name")))
		if name == "" {
			continue
		}
		get := func(f string) string {
			if k(f) == "" {
				return ""
			}
			return strings.TrimSpace(p.Meta.First(k(f)))
		}
		email, site := strings.ToLower(get("email")), get("url")
		id := strings.ToLower(strings.Join(strings.Fields(name), " ")) + "|" + email
		if email == "" {
			id += "|" + strings.ToLower(site)
		}
		sum := sha1.Sum([]byte(id))
		key := s.cfg.Source + ":guest:" + hex.EncodeToString(sum[:])[:12]
		s.guestKey[p.ID] = key
		if seen[key] {
			continue
		}
		seen[key] = true
		slug := get("slug")
		if !validSlug(slug) {
			slug = Slugify(name)
		}
		if slug == "" {
			slug = "guest-" + hex.EncodeToString(sum[:])[:6]
		}
		a := &records.Author{LegacyKey: key, Slug: uniq(slug, slugs), DisplayName: name, Bio: get("bio")}
		if strings.HasPrefix(site, "http://") || strings.HasPrefix(site, "https://") {
			a.Website = site
		}
		if av := get("avatar"); av != "" {
			a.AvatarMedia = s.resolveImage(av, av)
		}
		out = append(out, a)
	}
	return out
}

// postAuthors applies the guest-author mode: replace shows only the guest, join lists the WP author first.
func (s *state) postAuthors(p *extract.Post) []string {
	var out []string
	wpKey, hasWP := s.authorKey[p.Author]
	guest, hasGuest := s.guestKey[p.ID]
	switch {
	case hasGuest && s.set.GuestMode == "replace":
		out = []string{guest}
	case hasGuest && hasWP:
		out = []string{wpKey, guest}
	case hasGuest:
		out = []string{guest}
	case hasWP:
		out = []string{wpKey}
	}
	return out
}
