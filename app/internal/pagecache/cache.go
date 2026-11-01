// Package pagecache is the in-process LRU for rendered public pages (build plan §6.3). It is bounded by bytes, entries
// expire after the page TTL, and entries are grouped by base path so one purge drops an archive's pages and feed.
package pagecache

import (
	"container/list"
	"sync"
	"time"
)

// entryOverhead approximates the map, list and struct cost of one entry.
const entryOverhead = 256

type Entry struct {
	ContentType string
	Body        []byte
	ETag        string
	Group       string // base path that purges this entry
	Stored      time.Time
	Expires     time.Time
}

func (e *Entry) size(key string) int64 {
	return int64(len(e.Body)+len(key)+len(e.Group)+len(e.ETag)+len(e.ContentType)) + entryOverhead
}

type item struct {
	key   string
	entry *Entry
	size  int64
}

type Cache struct {
	mu       sync.Mutex
	max      int64
	maxEntry int64
	size     int64
	gen      uint64
	ll       *list.List // front is most recently used
	items    map[string]*list.Element
	groups   map[string]map[string]struct{}
	now      func() time.Time
}

// New returns a cache holding at most maxBytes, refusing single entries over maxEntry bytes.
func New(maxBytes, maxEntry int64) *Cache {
	return &Cache{max: maxBytes, maxEntry: maxEntry, ll: list.New(), items: map[string]*list.Element{},
		groups: map[string]map[string]struct{}{}, now: time.Now}
}

// Get returns a live entry and marks it recently used. Expired entries are dropped.
func (c *Cache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	it := el.Value.(*item)
	if !c.now().Before(it.entry.Expires) {
		c.remove(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return it.entry, true
}

// Gen changes on every purge. Capture it before rendering and pass it to Put, so a render that raced a purge
// is not stored.
func (c *Cache) Gen() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// Put stores e under key unless a purge happened since gen or e is too large. It evicts least recently used entries
// to make room.
func (c *Cache) Put(key string, e *Entry, gen uint64) bool {
	n := e.size(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen || n > c.maxEntry || n > c.max {
		return false
	}
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
	for c.size+n > c.max {
		c.remove(c.ll.Back())
	}
	c.items[key] = c.ll.PushFront(&item{key: key, entry: e, size: n})
	c.size += n
	g := c.groups[e.Group]
	if g == nil {
		g = map[string]struct{}{}
		c.groups[e.Group] = g
	}
	g[key] = struct{}{}
	return true
}

// Purge drops every entry in the given groups.
func (c *Cache) Purge(groups ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for _, g := range groups {
		for key := range c.groups[g] {
			c.remove(c.items[key])
		}
	}
}

func (c *Cache) PurgeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.ll.Init()
	c.items, c.groups, c.size = map[string]*list.Element{}, map[string]map[string]struct{}{}, 0
}

// Stats returns the entry count and the accounted size in bytes.
func (c *Cache) Stats() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.size
}

func (c *Cache) remove(el *list.Element) {
	it := c.ll.Remove(el).(*item)
	delete(c.items, it.key)
	c.size -= it.size
	if g := c.groups[it.entry.Group]; g != nil {
		delete(g, it.key)
		if len(g) == 0 {
			delete(c.groups, it.entry.Group)
		}
	}
}
