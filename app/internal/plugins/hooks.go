package plugins

import (
	"context"
	"html/template"
	"maps"
	"slices"
	"time"

	"afsc/internal/seo"
	"afsc/internal/spam"
	"afsc/internal/theme"
)

// Filter is a typed hook: a value of type T goes through every enabled handler in priority order, each seeing
// read-only args A. Clone copies T so a late or panicking handler can't change the value the next one gets.
type Filter[T, A any] struct {
	Name    string
	Timeout time.Duration
	Clone   func(T) T
}

const renderTimeout = 100 * time.Millisecond

// FeedItem is one RSS <item> before it's written.
type FeedItem struct {
	Title           string
	Link            string
	GUID            string
	GUIDIsPermaLink bool
	PubDate         time.Time
	Creators        []string
	Categories      []string
	Description     string // plain text
	ContentHTML     string
}

// SitemapArgs names the sitemap page an entries filter runs for: Type is post, page, category, tag, author or video.
type SitemapArgs struct {
	Type string
	Page int
}

var (
	HeadTags = Filter[template.HTML, *theme.PageData]{Name: "head.tags", Timeout: renderTimeout}
	BodyEnd  = Filter[template.HTML, *theme.PageData]{Name: "body.end", Timeout: renderTimeout}
	// ContentHTML runs on the main post of single, page and front_page renders, not on teasers or feeds.
	ContentHTML    = Filter[template.HTML, *theme.PageData]{Name: "content.html", Timeout: renderTimeout}
	SEOMeta        = Filter[*seo.Meta, *theme.PageData]{Name: "seo.meta", Timeout: renderTimeout, Clone: cloneMeta}
	SEOSchemaGraph = Filter[[]map[string]any, *theme.PageData]{Name: "seo.schema_graph", Timeout: renderTimeout,
		Clone: cloneGraph}
	SitemapEntries = Filter[[]seo.SitemapURL, SitemapArgs]{Name: "sitemap.entries", Timeout: 500 * time.Millisecond,
		Clone: slices.Clone[[]seo.SitemapURL]}
	FeedItemFilter = Filter[*FeedItem, *theme.Post]{Name: "feed.item", Timeout: renderTimeout, Clone: cloneFeedItem}
	// CSPDirectives maps a directive to extra sources. It runs once per settings snapshot, not per request.
	CSPDirectives = Filter[map[string][]string, struct{}]{Name: "csp.directives", Timeout: renderTimeout,
		Clone: cloneDirectives}
	// CommentCheck fails closed and its timeout covers the whole chain; run it with Runtime.CheckComment.
	CommentCheck = Filter[spam.Verdict, spam.Input]{Name: "comment.check", Timeout: 2 * time.Second, Clone: cloneVerdict}
)

// FilterNames lists every filter, for docs and registration checks.
var FilterNames = []string{HeadTags.Name, BodyEnd.Name, ContentHTML.Name, SEOMeta.Name, SEOSchemaGraph.Name,
	SitemapEntries.Name, FeedItemFilter.Name, CSPDirectives.Name, CommentCheck.Name}

func cloneMeta(m *seo.Meta) *seo.Meta {
	if m == nil {
		return nil
	}
	c := *m
	c.Tags = slices.Clone(m.Tags)
	return &c
}

func cloneGraph(g []map[string]any) []map[string]any {
	out := make([]map[string]any, len(g))
	for i, n := range g {
		out[i] = maps.Clone(n)
	}
	return out
}

func cloneFeedItem(f *FeedItem) *FeedItem {
	if f == nil {
		return nil
	}
	c := *f
	c.Creators, c.Categories = slices.Clone(f.Creators), slices.Clone(f.Categories)
	return &c
}

func cloneVerdict(v spam.Verdict) spam.Verdict {
	v.Reasons = slices.Clone(v.Reasons)
	return v
}

func cloneDirectives(m map[string][]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = slices.Clone(v)
	}
	return out
}

type handler struct {
	plugin string
	prio   int
	seq    int
	fn     any
}

// AddFilter registers fn on f. Lower prio runs first; ties keep registry order, then registration order.
func AddFilter[T, A any](h *Host, f Filter[T, A], prio int, fn func(ctx context.Context, v T, a A) (T, error)) {
	h.addHandler(f.Name, prio, fn)
}

// ApplyFilter runs v through f's enabled handlers. A handler that errors, panics or times out is skipped: the
// value it was given goes on to the next handler (fail-open). A nil runtime returns v.
func ApplyFilter[T, A any](ctx context.Context, rt *Runtime, f Filter[T, A], v T, a A) T {
	if rt == nil {
		return v
	}
	for _, hd := range rt.filters[f.Name] {
		if !rt.Enabled(hd.plugin) {
			continue
		}
		fn := hd.fn.(func(context.Context, T, A) (T, error))
		in := v
		if f.Clone != nil {
			in = f.Clone(v)
		}
		out, err := call(ctx, f.Timeout, func(ctx context.Context) (T, error) { return fn(ctx, in, a) })
		if err != nil {
			rt.logFailure(hd.plugin, f.Name, err)
			continue
		}
		v = out
	}
	return v
}

// HasFilter reports whether any handler is registered on name, enabled or not.
func (rt *Runtime) HasFilter(name string) bool {
	return rt != nil && len(rt.filters[name]) > 0
}
