// Package plugins describes the optional plugin adapters: how each is detected and which meta keys and options
// it reads. Defaults are the plugins' publicly known keys; every one can be overridden in afsc-wp.json and must be
// verified against the site's inventory (docs/config.md).
package plugins

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/extract"
)

type Adapter struct {
	ID          string
	Name        string
	PluginFiles []string // active_plugins directory prefixes
	MetaPrefix  string   // a postmeta/usermeta key prefix that proves the plugin left data
	Keys        map[string]string
}

var Adapters = []Adapter{
	{ID: "core", Name: "WordPress core", Keys: map[string]string{
		"thumbnail": "_thumbnail_id", "page_template": "_wp_page_template", "old_slug": "_wp_old_slug",
		"old_date": "_wp_old_date", "attached_file": "_wp_attached_file", "attachment_metadata": "_wp_attachment_metadata",
		"image_alt": "_wp_attachment_image_alt", "user_bio": "description", "first_name": "first_name", "last_name": "last_name",
	}},
	{ID: "yoast", Name: "Yoast SEO", PluginFiles: []string{"wordpress-seo/", "wordpress-seo-premium/"}, MetaPrefix: "_yoast_wpseo_", Keys: map[string]string{
		"title": "_yoast_wpseo_title", "description": "_yoast_wpseo_metadesc", "focus_keyphrase": "_yoast_wpseo_focuskw",
		"canonical": "_yoast_wpseo_canonical", "noindex": "_yoast_wpseo_meta-robots-noindex",
		"nofollow": "_yoast_wpseo_meta-robots-nofollow", "robots_advanced": "_yoast_wpseo_meta-robots-adv",
		"breadcrumb_title": "_yoast_wpseo_bctitle", "primary_category": "_yoast_wpseo_primary_category",
		"cornerstone": "_yoast_wpseo_is_cornerstone", "og_title": "_yoast_wpseo_opengraph-title",
		"og_description": "_yoast_wpseo_opengraph-description", "og_image": "_yoast_wpseo_opengraph-image",
		"og_image_id": "_yoast_wpseo_opengraph-image-id", "twitter_title": "_yoast_wpseo_twitter-title",
		"twitter_description": "_yoast_wpseo_twitter-description", "twitter_image": "_yoast_wpseo_twitter-image",
		"twitter_image_id": "_yoast_wpseo_twitter-image-id", "redirect": "_yoast_wpseo_redirect",
		"opt_titles": "wpseo_titles", "opt_social": "wpseo_social", "opt_main": "wpseo", "opt_taxonomy_meta": "wpseo_taxonomy_meta",
		"author_title": "wpseo_title", "author_description": "wpseo_metadesc", "author_noindex": "wpseo_noindex_author",
		"author_social": "facebook,instagram,linkedin,myspace,pinterest,soundcloud,tumblr,twitter,youtube,wikipedia",
	}},
	{ID: "yoast_premium", Name: "Yoast SEO Premium redirects", PluginFiles: []string{"wordpress-seo-premium/"}, Keys: map[string]string{
		"opt_redirects": "wpseo-premium-redirects-base", "opt_export_plain": "wpseo-premium-redirects-export-plain",
		"opt_export_regex": "wpseo-premium-redirects-export-regex",
	}},
	{ID: "yoast_video", Name: "Yoast Video SEO", PluginFiles: []string{"wpseo-video/"}, MetaPrefix: "_yoast_wpseo_video", Keys: map[string]string{
		"video_meta": "_yoast_wpseo_video_meta", "not_family_friendly": "_yoast_wpseo_videositemap-not-family-friendly",
		"disable": "_yoast_wpseo_videositemap-disable", "opt_video": "wpseo_video",
	}},
	{ID: "jetpack", Name: "Jetpack", PluginFiles: []string{"jetpack/"}, Keys: map[string]string{
		"opt_modules": "jetpack_active_modules", "cdn_hosts": "i0.wp.com,i1.wp.com,i2.wp.com,i3.wp.com",
		"shortcodes": "youtube,vimeo,tweet,twitter-timeline,instagram,spotify,soundcloud,googlemaps,dailymotion,slideshare,gist,ted,flickr,scribd,recipe,archives,wufoo,brightcove,crowdsignal,polldaddy",
	}},
	{ID: "user_profile_picture", Name: "User Profile Picture", PluginFiles: []string{"metronet-profile-picture/"}, MetaPrefix: "metronet_", Keys: map[string]string{
		"avatar": "metronet_image_id",
	}},
	// Guest-author plugins differ; nothing is assumed. Keys must be configured (name is required).
	{ID: "guest_author", Name: "Guest author", Keys: map[string]string{
		"name": "", "email": "", "url": "", "bio": "", "slug": "", "avatar": "",
	}},
}

// Set is the resolved adapter state for one site.
type Set struct {
	on        map[string]bool
	keys      map[string]map[string]string
	reasons   map[string]string
	GuestMode string
}

// Resolve applies config overrides, then auto-detects from active_plugins and the meta-key inventory.
func Resolve(cfg *config.Config, activePlugins []string, metaKeys []extract.Count) *Set {
	s := &Set{on: map[string]bool{}, keys: map[string]map[string]string{}, reasons: map[string]string{}, GuestMode: "join"}
	for _, a := range Adapters {
		keys := map[string]string{}
		for k, v := range a.Keys {
			keys[k] = v
		}
		ov := cfg.PluginOverride(a.ID)
		for k, v := range ov.Keys {
			keys[k] = v
		}
		s.keys[a.ID] = keys
		switch {
		case ov.Enabled != nil:
			s.on[a.ID], s.reasons[a.ID] = *ov.Enabled, "set in afsc-wp.json"
		case a.ID == "core":
			s.on[a.ID], s.reasons[a.ID] = true, "always"
		case a.ID == "guest_author":
			name := keys["name"]
			s.on[a.ID] = name != "" && hasKey(metaKeys, name)
			s.reasons[a.ID] = "configured meta key present in postmeta"
			if name == "" {
				s.reasons[a.ID] = "no plugins.guest_author.keys.name configured"
			}
		default:
			if p := matchPlugin(a.PluginFiles, activePlugins); p != "" {
				s.on[a.ID], s.reasons[a.ID] = true, "active plugin "+p
			} else if a.MetaPrefix != "" && hasPrefix(metaKeys, a.MetaPrefix) {
				s.on[a.ID], s.reasons[a.ID] = true, "meta keys with prefix "+a.MetaPrefix
			} else {
				s.reasons[a.ID] = "not detected"
			}
		}
	}
	if m := cfg.PluginOverride("guest_author").Mode; m != "" {
		s.GuestMode = m
	}
	return s
}

func matchPlugin(prefixes, active []string) string {
	for _, a := range active {
		for _, p := range prefixes {
			if strings.HasPrefix(a, p) {
				return a
			}
		}
	}
	return ""
}

func hasKey(counts []extract.Count, key string) bool {
	return slices.ContainsFunc(counts, func(c extract.Count) bool { return c.Key == key })
}

func hasPrefix(counts []extract.Count, prefix string) bool {
	return slices.ContainsFunc(counts, func(c extract.Count) bool { return strings.HasPrefix(c.Key, prefix) })
}

func (s *Set) On(id string) bool { return s.on[id] }

// Key returns the configured meta key or option name for an adapter field.
func (s *Set) Key(id, name string) string {
	k, ok := s.keys[id][name]
	if !ok {
		panic(fmt.Sprintf("plugins: unknown key %s.%s", id, name))
	}
	return k
}

// List splits a comma-separated key value.
func (s *Set) List(id, name string) []string {
	var out []string
	for _, v := range strings.Split(s.Key(id, name), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

type Status struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Reason  string            `json:"reason"`
	Keys    map[string]string `json:"keys"`
}

// Report lists every adapter with its state and effective keys, for inventory.md.
func (s *Set) Report() []Status {
	var out []Status
	for _, a := range Adapters {
		out = append(out, Status{ID: a.ID, Name: a.Name, Enabled: s.on[a.ID], Reason: s.reasons[a.ID], Keys: s.keys[a.ID]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// OptionNames are the options enabled adapters read, so extract loads them.
func OptionNames(s *Set) []string {
	var out []string
	for _, a := range Adapters {
		for k, v := range s.keys[a.ID] {
			if strings.HasPrefix(k, "opt_") && v != "" {
				out = append(out, v)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// UserMetaKeys are the usermeta keys extract keeps; every other usermeta key is skipped (it may hold secrets).
func UserMetaKeys(s *Set, prefix string) []string {
	out := []string{prefix + "capabilities", prefix + "user_level", "nickname"}
	out = append(out, s.Key("core", "user_bio"), s.Key("core", "first_name"), s.Key("core", "last_name"))
	out = append(out, s.Key("yoast", "author_title"), s.Key("yoast", "author_description"), s.Key("yoast", "author_noindex"))
	out = append(out, s.List("yoast", "author_social")...)
	out = append(out, s.Key("user_profile_picture", "avatar"))
	slices.Sort(out)
	return slices.Compact(out)
}
