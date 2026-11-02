package convert

import (
	"net/url"
	"regexp"
	"strings"

	"afsc/internal/content"
)

var (
	ytIDRe      = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)
	digitsRe    = regexp.MustCompile(`^\d{1,20}$`)
	spotifyKind = map[string]bool{"track": true, "album": true, "playlist": true, "episode": true, "show": true, "artist": true}
	spotifyID   = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
)

// EmbedFromURL maps a provider URL (page, share or iframe form) to an AFSC embed node.
func EmbedFromURL(raw string) (*content.Node, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "spotify:") {
		parts := strings.Split(raw, ":")
		if len(parts) == 3 && spotifyKind[parts[1]] && spotifyID.MatchString(parts[2]) {
			return embed("spotify", "https://open.spotify.com/"+parts[1]+"/"+parts[2], ""), true
		}
		return nil, false
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	seg := func(i int) string {
		if i < len(segs) {
			return segs[i]
		}
		return ""
	}
	switch host {
	case "youtube.com", "m.youtube.com", "youtube-nocookie.com", "music.youtube.com":
		id := u.Query().Get("v")
		if seg(0) == "embed" || seg(0) == "shorts" || seg(0) == "live" || seg(0) == "v" {
			id = seg(1)
		}
		if ytIDRe.MatchString(id) {
			return embed("youtube", "https://www.youtube.com/watch?v="+id, id), true
		}
	case "youtu.be":
		if id := seg(0); ytIDRe.MatchString(id) {
			return embed("youtube", "https://www.youtube.com/watch?v="+id, id), true
		}
	case "vimeo.com", "player.vimeo.com":
		for _, s := range segs {
			if digitsRe.MatchString(s) {
				return embed("vimeo", "https://vimeo.com/"+s, s), true
			}
		}
	case "twitter.com", "x.com", "mobile.twitter.com":
		if len(segs) >= 3 && (segs[1] == "status" || segs[1] == "statuses") && digitsRe.MatchString(segs[2]) {
			return embed("x", "https://x.com/"+segs[0]+"/status/"+segs[2], ""), true
		}
	case "instagram.com", "instagr.am":
		if (seg(0) == "p" || seg(0) == "reel" || seg(0) == "tv") && seg(1) != "" {
			return embed("instagram", "https://www.instagram.com/"+seg(0)+"/"+seg(1)+"/", ""), true
		}
	case "open.spotify.com":
		if seg(0) == "embed" {
			segs = segs[1:]
		}
		if len(segs) >= 2 && spotifyKind[segs[0]] && spotifyID.MatchString(segs[1]) {
			return embed("spotify", "https://open.spotify.com/"+segs[0]+"/"+segs[1], ""), true
		}
	case "soundcloud.com", "m.soundcloud.com":
		if len(segs) >= 1 {
			return embed("soundcloud", "https://soundcloud.com/"+strings.Join(segs, "/"), ""), true
		}
	case "w.soundcloud.com":
		if inner := u.Query().Get("url"); inner != "" && !strings.Contains(inner, "w.soundcloud.com") {
			return EmbedFromURL(inner)
		}
	case "google.com", "maps.google.com":
		if strings.HasPrefix(u.Path, "/maps/embed") && u.RawQuery != "" {
			return embed("googlemaps", "https://www.google.com/maps/embed?"+u.RawQuery, ""), true
		}
	}
	return nil, false
}

func embed(provider, u, id string) *content.Node {
	attrs := map[string]any{"provider": provider, "url": u}
	if id != "" {
		attrs["externalId"] = id
	}
	return &content.Node{Type: "embed", Attrs: attrs}
}

// IframeHost returns the lowercased host of an iframe src.
func IframeHost(src string) string {
	if strings.HasPrefix(src, "//") {
		src = "https:" + src
	}
	u, err := url.Parse(src)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
