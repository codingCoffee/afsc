// Package share gives themes share links for a post with plg_share_links (build plan §14). It makes no requests and
// loads nothing: the links are plain URLs, and icons come from the theme.
package share

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strings"

	"afsc/internal/plugins"
	"afsc/internal/theme"
)

// Link is one share link. URL is a plain string, so html/template escapes it in href.
type Link struct {
	Network string
	Label   string
	URL     string
}

type network struct {
	id, label string
	build     func(link, title string) string
}

// Networks are the supported networks in the order links are returned.
var Networks = []string{"x", "linkedin", "facebook", "whatsapp", "telegram", "email"}

var networks = []network{
	{"x", "X", func(u, t string) string { return "https://x.com/intent/post?url=" + esc(u) + "&text=" + esc(t) }},
	{"linkedin", "LinkedIn", func(u, _ string) string { return "https://www.linkedin.com/sharing/share-offsite/?url=" + esc(u) }},
	{"facebook", "Facebook", func(u, _ string) string { return "https://www.facebook.com/sharer/sharer.php?u=" + esc(u) }},
	{"whatsapp", "WhatsApp", func(u, t string) string { return "https://wa.me/?text=" + esc(t+" "+u) }},
	{"telegram", "Telegram", func(u, t string) string { return "https://t.me/share/url?url=" + esc(u) + "&text=" + esc(t) }},
	{"email", "Email", func(u, t string) string { return "mailto:?subject=" + esc(t) + "&body=" + esc(u) }},
}

// esc percent-encodes a query value with %20 for spaces, which mailto: needs and the share endpoints accept.
func esc(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

type Plugin struct{}

func New() Plugin { return Plugin{} }

func (Plugin) ID() string { return "share" }

func (Plugin) Info() plugins.Info {
	return plugins.Info{Name: "Share links", DefaultEnabled: true,
		Description: "Share links for X, LinkedIn, Facebook, WhatsApp, Telegram and email. Themes place them with plg_share_links; nothing loads from those sites."}
}

func (Plugin) Init(_ context.Context, h *plugins.Host) error {
	h.RegisterTemplateFuncs(template.FuncMap{"plg_share_links": Links})
	return nil
}

// Links is plg_share_links: share links for p, for every network or only the ones named. An unknown name is an
// error, so `afsc theme check` catches typos.
func Links(p *theme.Post, only ...string) ([]Link, error) {
	for _, n := range only {
		if !slices.Contains(Networks, n) {
			return nil, fmt.Errorf("plg_share_links: unknown network %q (use %s)", n, strings.Join(Networks, ", "))
		}
	}
	if p == nil || p.URL == "" {
		return nil, nil
	}
	out := make([]Link, 0, len(networks))
	for _, n := range networks {
		if len(only) == 0 || slices.Contains(only, n.id) {
			out = append(out, Link{Network: n.id, Label: n.label, URL: n.build(p.URL, p.Title)})
		}
	}
	return out, nil
}
