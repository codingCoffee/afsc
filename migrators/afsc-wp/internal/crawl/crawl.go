// Package crawl is the polite baseline crawler (migration plan §4): two workers, a delay between requests,
// a timeout on every request, an identifying User-Agent, and never a request to a host outside the site.
package crawl

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/htmlmeta"
)

const Workers = 2

type Options struct {
	Base      string
	Hosts     []string // hostnames treated as the site, besides the base URL's host
	UserAgent string
	Delay     time.Duration
	Timeout   time.Duration
	MaxDepth  int
	MaxURLs   int
	Selector  string
	Seeds     []string // extra URLs or paths (analytics, GSC, route samples)
	OutDir    string   // baseline/
	Client    *http.Client
	Logf      func(string, ...any)
}

type Hop struct {
	Status   int    `json:"status"`
	Location string `json:"location,omitempty"`
}

type Record struct {
	URL         string            `json:"url"`
	Depth       int               `json:"depth"`
	Status      int               `json:"status"`
	Chain       []Hop             `json:"chain,omitempty"`
	Final       string            `json:"final"`
	FinalStatus int               `json:"final_status"`
	Headers     map[string]string `json:"headers,omitempty"`
	Meta        *htmlmeta.Meta    `json:"meta,omitempty"`
	Error       string            `json:"error,omitempty"`
}

var keepHeaders = []string{"Content-Type", "Cache-Control", "Last-Modified", "ETag", "X-Robots-Tag", "Link", "Location"}

type crawler struct {
	o        Options
	base     *url.URL
	hosts    map[string]bool
	disallow []string
	client   *http.Client
	mu       sync.Mutex
	seen     map[string]bool
	out      []Record
}

func (c *crawler) logf(f string, a ...any) {
	if c.o.Logf != nil {
		c.o.Logf(f, a...)
	}
}

// OnSite reports whether u is on one of the site's hosts.
func (c *crawler) onSite(u *url.URL) bool {
	return (u.Scheme == "http" || u.Scheme == "https") && (c.hosts[strings.ToLower(u.Host)] || c.hosts[strings.ToLower(u.Hostname())])
}

// allowed honours robots.txt only for admin and login paths, as the plan asks.
func (c *crawler) allowed(u *url.URL) bool {
	p := u.EscapedPath()
	for _, d := range c.disallow {
		if strings.HasPrefix(p, d) {
			return false
		}
	}
	return true
}

func Run(ctx context.Context, o Options) ([]Record, error) {
	base, err := url.Parse(o.Base)
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("crawl base URL %q is invalid", o.Base)
	}
	c := &crawler{o: o, base: base, hosts: map[string]bool{strings.ToLower(base.Host): true}, seen: map[string]bool{}, client: o.Client}
	for _, h := range o.Hosts {
		c.hosts[strings.ToLower(h)] = true
	}
	if c.client == nil {
		c.client = &http.Client{}
	}
	cl := *c.client
	cl.Timeout = o.Timeout
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.client = &cl
	if err := os.MkdirAll(filepath.Join(o.OutDir, "sitemaps"), 0o700); err != nil {
		return nil, err
	}

	sitemaps := c.robots(ctx)
	var seeds []string
	seeds = append(seeds, base.ResolveReference(&url.URL{Path: "/"}).String(), base.ResolveReference(&url.URL{Path: "/feed/"}).String())
	seeds = append(seeds, c.sitemaps(ctx, append(sitemaps, base.ResolveReference(&url.URL{Path: "/sitemap_index.xml"}).String()))...)
	for _, s := range o.Seeds {
		seeds = append(seeds, base.ResolveReference(mustParse(s)).String())
	}
	c.save(ctx, "/feed/", "feed.xml")
	c.bfs(ctx, seeds)
	sort.Slice(c.out, func(i, j int) bool { return c.out[i].URL < c.out[j].URL })
	return c.out, extract.WriteNDJSON(filepath.Join(o.OutDir, "urls.ndjson"), c.out)
}

func mustParse(s string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return &url.URL{Path: "/"}
	}
	return u
}

func (c *crawler) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.o.UserAgent)
	return c.client.Do(req)
}

func (c *crawler) pause(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(c.o.Delay):
	}
}

func (c *crawler) robots(ctx context.Context) []string {
	resp, err := c.get(ctx, c.base.ResolveReference(&url.URL{Path: "/robots.txt"}).String())
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	os.WriteFile(filepath.Join(c.o.OutDir, "robots.txt"), body, 0o600)
	var maps []string
	applies := false
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			applies = v == "*"
		case "disallow":
			lv := strings.ToLower(v)
			if applies && v != "" && (strings.Contains(lv, "admin") || strings.Contains(lv, "login")) {
				c.disallow = append(c.disallow, v)
			}
		case "sitemap":
			maps = append(maps, v)
		}
	}
	c.pause(ctx)
	return maps
}

type sitemapDoc struct {
	XMLName  xml.Name
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// sitemaps fetches sitemap indexes and child sitemaps, saving each file, and returns every page URL.
func (c *crawler) sitemaps(ctx context.Context, roots []string) []string {
	var urls []string
	done := map[string]bool{}
	queue := roots
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		pu, err := url.Parse(u)
		if err != nil || done[u] || !c.onSite(pu) {
			continue
		}
		done[u] = true
		resp, err := c.get(ctx, u)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		c.pause(ctx)
		if resp.StatusCode != http.StatusOK {
			continue
		}
		os.WriteFile(filepath.Join(c.o.OutDir, "sitemaps", path.Base(pu.Path)), body, 0o600)
		var doc sitemapDoc
		if xml.Unmarshal(body, &doc) != nil {
			continue
		}
		for _, s := range doc.Sitemaps {
			queue = append(queue, strings.TrimSpace(s.Loc))
		}
		for _, x := range doc.URLs {
			urls = append(urls, strings.TrimSpace(x.Loc))
		}
	}
	return urls
}

func (c *crawler) save(ctx context.Context, p, name string) {
	resp, err := c.get(ctx, c.base.ResolveReference(&url.URL{Path: p}).String())
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		os.WriteFile(filepath.Join(c.o.OutDir, name), body, 0o600)
	}
	c.pause(ctx)
}

type job struct {
	url   string
	depth int
}

func (c *crawler) claim(u string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen[u] || (c.o.MaxURLs > 0 && len(c.seen) >= c.o.MaxURLs) {
		return false
	}
	c.seen[u] = true
	return true
}

func (c *crawler) bfs(ctx context.Context, seeds []string) {
	jobs := make(chan job, 1<<16)
	var pending sync.WaitGroup
	enqueue := func(u string, depth int) {
		pu, err := url.Parse(u)
		if err != nil || !c.onSite(pu) || !c.allowed(pu) {
			return
		}
		pu.Fragment = ""
		if !c.claim(pu.String()) {
			return
		}
		pending.Add(1)
		select {
		case jobs <- job{pu.String(), depth}:
		default:
			pending.Done()
		}
	}
	for _, s := range seeds {
		enqueue(s, 0)
	}
	var wg sync.WaitGroup
	for range Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				rec, links := c.fetch(ctx, j)
				c.mu.Lock()
				c.out = append(c.out, rec)
				c.mu.Unlock()
				if j.depth < c.o.MaxDepth {
					for _, l := range links {
						enqueue(l, j.depth+1)
					}
				}
				pending.Done()
				c.pause(ctx)
			}
		}()
	}
	pending.Wait()
	close(jobs)
	wg.Wait()
}

// fetch follows redirects by hand (recording each hop) without leaving the site, then reads the final page.
func (c *crawler) fetch(ctx context.Context, j job) (Record, []string) {
	rec := Record{URL: j.url, Depth: j.depth}
	cur := j.url
	for hop := 0; ; hop++ {
		resp, err := c.get(ctx, cur)
		if err != nil {
			rec.Error = err.Error()
			return rec, nil
		}
		if hop == 0 {
			rec.Status = resp.StatusCode
			rec.Headers = map[string]string{}
			for _, h := range keepHeaders {
				if v := resp.Header.Get(h); v != "" {
					rec.Headers[h] = v
				}
			}
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
			resp.Body.Close()
			loc, _ := url.Parse(cur)
			next := loc.ResolveReference(mustParse(resp.Header.Get("Location")))
			rec.Chain = append(rec.Chain, Hop{Status: resp.StatusCode, Location: next.String()})
			if !c.onSite(next) || hop >= 9 {
				rec.Final, rec.FinalStatus = next.String(), 0
				return rec, nil
			}
			cur = next.String()
			c.pause(ctx)
			continue
		}
		defer resp.Body.Close()
		rec.Final, rec.FinalStatus = cur, resp.StatusCode
		if !strings.Contains(resp.Header.Get("Content-Type"), "html") {
			return rec, nil
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			rec.Error = err.Error()
			return rec, nil
		}
		m, err := htmlmeta.Parse(body, c.o.Selector)
		if err == nil {
			rec.Meta = m
		}
		return rec, c.links(cur, body)
	}
}

var skipPrefixes = []string{"/wp-admin", "/wp-login", "/wp-json", "/xmlrpc.php", "/wp-content/", "/wp-includes/", "/feed/atom", "/comments/feed"}

func (c *crawler) links(page string, body []byte) []string {
	base, _ := url.Parse(page)
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key != "href" {
					continue
				}
				u, err := url.Parse(strings.TrimSpace(a.Val))
				if err != nil {
					continue
				}
				u = base.ResolveReference(u)
				u.Fragment = ""
				if u.RawQuery != "" && u.Path != "/" {
					u.RawQuery = ""
				}
				skip := false
				for _, p := range skipPrefixes {
					skip = skip || strings.HasPrefix(u.Path, p)
				}
				if !skip && c.onSite(u) {
					out = append(out, u.String())
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	return out
}

// ReadURLFile reads URLs from a text or CSV export (analytics, Search Console): the first column that looks like
// a URL or a path, header rows skipped.
func ReadURLFile(p string) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var out []string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, cell := range rec {
			cell = strings.TrimSpace(cell)
			if strings.HasPrefix(cell, "http://") || strings.HasPrefix(cell, "https://") || strings.HasPrefix(cell, "/") {
				out = append(out, cell)
				break
			}
		}
	}
	return out, nil
}

// Load reads baseline/urls.ndjson.
func Load(dir string) ([]Record, error) {
	return extract.ReadNDJSON[Record](filepath.Join(dir, "urls.ndjson"))
}
