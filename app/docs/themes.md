# Themes

A theme is a directory of Go `html/template` files, a `theme.json` manifest and static assets. Themes live in git and
are deployed with the binary; they can't be edited from the admin. `themes/default` is the reference theme.

```
theme.json
templates/
  base.html                 optional shared layout
  index.html                required; the final fallback for every page
  front-page.html  home.html
  single.html  single-{slug}.html
  page.html  page-{slug}.html  page-{template}.html
  archive.html  category.html  category-{slug}.html  tag.html  tag-{slug}.html
  author.html  author-{slug}.html  date.html
  search.html  404.html  410.html
  parts/*.html              partials, called as {{template "parts/name.html" .}}
  nodes/*.html              content node overrides
assets/                     css, js, fonts, images; served at content-hashed URLs
```

Point `AFSC_THEME_DIR` at the directory. With `AFSC_ENV=dev`, the server re-reads the theme within half a second of a
file changing. A broken edit shows the error page; the last good version stays loaded. In production, a theme that
fails to load stops the server from starting.

## theme.json

Unknown fields are errors.

```json
{
  "name": "default",
  "version": "0.2.0",
  "image_sizes": {"thumbnail": {"width": 150, "height": 150, "crop": true}},
  "srcset_widths": [320, 640, 960, 1280, 1920],
  "menu_locations": ["primary", "footer"],
  "content": {"allowed_classes": ["lead", "callout"]},
  "assets": [{"path": "style.css"}, {"path": "app.js", "defer": true}],
  "csp": {"script": [], "style": [], "frame": [], "img": [], "font": [], "connect": []},
  "settings": {"show_reading_time": {"type": "bool", "label": "Show reading time", "default": true},
               "layout": {"type": "select", "label": "Layout", "default": "wide", "options": ["wide", "narrow"]}},
  "strings": {"read_more": "Continue reading"},
  "posts_per_page": {"home": 5, "category": 12}
}
```

| Field | Meaning |
|---|---|
| `name`, `version` | `name` is required. Bundles record the theme they were checked against. |
| `image_sizes` | Named sizes (`width`, `height`, `crop`). Without `crop`, the image is scaled down to fit the box, and 0 means unconstrained. With `crop`, both dimensions are required and the image is cut to exactly that size. `full` is reserved for the original. Dimensions go up to 10000. See [Images](#images). |
| `srcset_widths` | Extra widths offered in `srcset`, 1–10000. See [Images](#images). |
| `menu_locations` | Locations menus can be assigned to; `menu` only accepts these. |
| `content.allowed_classes` | Classes content may use (`classes` attributes). Unknown classes are rejected on save and dropped on import. |
| `assets` | Files under `assets/` the page head includes (`head`, M2). Each must exist. |
| `csp` | Extra origins per directive (`script`, `style`, `frame`, `img`, `font`, `connect`) for the public Content Security Policy. Each must be an `https://` origin, optionally with a leading `*.`. See [Content Security Policy](#content-security-policy). |
| `settings` | Theme options: `type` (`bool`, `string`, `number`, `select` with `options`, or `color` as `#rrggbb`), `label` and a `default` of that type. Admins edit them under Settings → Theme; `theme_setting` returns the admin's value, else the default. |
| `strings` | Static strings for `t`. They override the built-in defaults listed under `t`. |
| `posts_per_page` | Per-archive overrides of the site's posts-per-page: `home` (also the posts page), `category`, `tag`, `author`, `date`, `search`. 1–100. |

## Layout: base.html and blocks

Without `base.html`, every page template is a complete HTML document.

With `base.html`, every page is rendered through it. Page templates only `define` the blocks the layout declares:

```html
<!-- base.html -->
<!doctype html>
<html lang="{{.Site.Language}}">
<head><meta charset="utf-8">{{head .}}</head>
<body class="{{body_class .}}">{{block "content" .}}{{end}}{{footer .}}</body>
</html>

<!-- single.html -->
{{define "content"}}<article class="{{post_class .Post}}">{{.Post.ContentHTML}}</article>{{end}}
```

Every template file is parsed with `base.html`, all of `parts/` and all of `nodes/`, so any of them can call
`{{template "parts/x.html" .}}`. Named templates you `define` inside a part are shared the same way.

Missing map keys are errors (`missingkey=error`). Use `index .Attrs "classes"` when a key is optional.

## Template hierarchy

AFSC renders the first template that exists, most specific first. `{slug}` is the item's own slug, not its path.

| Request | Templates tried |
|---|---|
| `/` showing latest posts | `front-page` → `home` → `index` |
| `/` showing a static page (`reading.home`) | `front-page` → `page-{template}` → `page-{slug}` → `page` → `index` |
| Posts page (`reading.posts_page`, only when `reading.home` is set) | `home` → `index` |
| Post | `single-{slug}` → `single` → `index` |
| Page | `page-{template}` → `page-{slug}` → `page` → `index` |
| Category archive | `category-{slug}` → `category` → `archive` → `index` |
| Tag archive | `tag-{slug}` → `tag` → `archive` → `index` |
| Author archive | `author-{slug}` → `author` → `archive` → `index` |
| Year or month archive | `date` → `archive` → `index` |
| Search | `search` → `index` |
| Not found | `404` → `index` |
| Gone (410 or 451 redirect) | `410` → `404` → `index` |

`{template}` is the post's `template` field, for example `page-full-width.html`.

## URLs the site answers

Defaults shown; the bases, post pattern and trailing slash come from the `permalinks` settings.

| Path | Page |
|---|---|
| `/`, `/page/{n}/` | Home |
| `/{slug}/` (post pattern) | Post |
| `/{parent}/{child}/` | Page |
| `/category/{path}/`, `/tag/{slug}/`, `/author/{slug}/` | Archives, each with `page/{n}/` |
| `/{yyyy}/`, `/{yyyy}/{mm}/` | Date archives (when enabled) |
| `/feed/`, `{category, tag or author archive}/feed/` | RSS 2.0 |
| `/search/?q=…&page=n` | Search (always `noindex`, `Cache-Control: private, no-store`) |
| `/m/{id}/{v}/{variant}/{filename}` | Media files and image variants (see [Images](#images)) |
| Any path in `media_legacy_paths` | An imported WordPress upload URL, served with a 200 and never redirected |
| `/sitemap_index.xml` | Sitemap index; `/sitemap.xml` redirects here |
| `/{post,page,category,post_tag,author,video}-sitemap.xml`, then `…-sitemap2.xml`, `…-sitemap3.xml` | Sitemaps (see [Sitemaps and text files](#sitemaps-and-text-files)) |
| `/robots.txt` | `seo.robots_txt`, or allow-all plus the `Sitemap:` line |
| `/llms.txt` | Only when `seo.llms_txt.mode` is `auto` or `custom` |
| `/{key}.txt` | The IndexNow key file, only when `seo.indexnow.enabled` |

- **Precedence.** On the same path, date archives win, then category, tag and author bases, then pages, then posts.
- **Pagination.** `page/1/` redirects to the archive. A page past the last one is a 404.
- **Feed aliases.** `/feed/rss2/` and `/feed/atom/` redirect to `/feed/`. `{post}/feed/` redirects to the post.
- **Comment pages.** With `permalinks.wpcompat_enabled`, WordPress's `{post}/comment-page-N/` redirects to
  `{post}?cpage=N`. `POST /comments` takes comment submissions (see [Comments](#comments)).
- **Canonical URLs.**
  - Uppercase paths, duplicate slashes, an `/index.php` prefix and the wrong trailing slash all get a 301 to the
    canonical form. File-like paths such as `/robots.txt` keep no trailing slash.
  - A post whose URL tokens don't match (a wrong date or category) also gets a 301 to its canonical URL.
- **Visibility.** Only `published` posts whose publish time has passed are shown. Drafts, scheduled, private and
  trashed posts are 404s.
- **Request order.**
  1. Theme assets, then the sitemap and text-file paths above (exact, case-sensitive matches; they win over redirects
     and the trailing-slash policy), then `/m/` media URLs. A `/m/` URL naming no media item, or a variant the theme doesn't
     allow, is a 404.
  2. Legacy upload paths: an exact, case-sensitive match against `media_legacy_paths`, tried only when the last path
     segment has a file extension. A miss carries on down this list, so a missing `/wp-content/uploads/…` file can
     still be redirected or logged as a 404.
  3. WordPress query URLs on `/` (`?p=`, `?page_id=`, `?cat=`, `?author=`, `?attachment_id=`, `?feed=`, `?s=`).
     They 301 to the canonical URL; switch them off with `permalinks.wpcompat_enabled`.
  4. Exact redirects, matched against both the raw and the canonical path.
  5. The canonical 301.
  6. Content.
  7. Prefix redirects (longest first, to the target as-is), then regex redirects (RE2, `$1` expands).
  8. 404.
- **Not-found log.** 404s are counted in memory and written to `not_found_log` every 10 seconds, or sooner at 1,000
  distinct paths. Paths matching `seo.not_found.noise_patterns` (`*.php`, `/.env*`, `/wp-admin/*` and so on) are
  never logged.
- **Redirect hits.** Every redirect served in steps 4 and 7 adds to that redirect's `hits` and `last_hit_at`,
  written every 60 seconds. A CDN that caches redirects hides the hits it answers.

## Sitemaps and text files

These are built in and not themable. They follow the Yoast layout, so existing Search Console submissions keep
working.

- **Index.** `/sitemap_index.xml` lists every non-empty sitemap page, with the newest `lastmod` on it. A type with no
  URLs is left out of the index, and its sitemap returns 404.
- **Paging.** Each file holds `seo.sitemaps.page_size` URLs (default 1000). Page 1 has no number (`post-sitemap.xml`);
  later pages are `post-sitemap2.xml` and so on. Posts and pages are ordered oldest first by publish time, then id,
  so new posts land on the last page. Archives are ordered by id.
- **What each type lists.**

  | Sitemap | URLs | `lastmod` |
  |---|---|---|
  | `post` | Published posts | `updated_at` |
  | `page` | Published pages. When the home page shows the latest posts, `/` comes first | `updated_at`; for `/`, the newest post's |
  | `category`, `post_tag` | Archives with at least one published post (a category counts its subcategories' posts) | The newest post's `updated_at` |
  | `author` | Archives with at least one published post. None when author archives are off | The newest post's `updated_at` |
  | `video` | Published posts and pages with a video that has a thumbnail, title and description | `updated_at` |

- **Left out.** Drafts, pending, scheduled, private and trashed posts, and posts whose publish time hasn't come; noindex entities (their own `robots_noindex`, else the
  type default such as `seo.tag_noindex`); entities whose `canonical` override points at a different URL (compared
  as an absolute URL, so a self-canonical with another trailing slash or scheme also counts as different); empty
  archives. There are no date archive sitemaps.
- **Images.** Post and page entries carry `image:image` for the featured image and every image node in the content
  (galleries included) that comes from the media library, as absolute `/m/` original URLs. Images given only as an
  external `src` are skipped. Only `image:loc` is written; Google ignores the other image tags.
- **Videos.** One `video:video` per video, using the same thumbnail as the page's JSON-LD (the thumbnail media,
  else the stored thumbnail URL). YouTube and Vimeo videos get `video:player_loc`; others get `video:content_loc`.
  Titles are cut to 100 characters and descriptions to 2,048. A duration over 8 hours is left out.
- **robots.txt.** `seo.robots_txt` is served exactly as written. When it's empty, the default is:

  ```
  User-agent: *
  Disallow:

  Sitemap: https://blog.example.com/sitemap_index.xml
  ```

- **llms.txt.** Off by default. `custom` serves `seo.llms_txt.body` as written. `auto` follows
  [llmstxt.org](https://llmstxt.org): the site title as the heading, the tagline as a quote, then a Pages list
  (top-level pages) and a Categories list (top-level categories, with their descriptions). It applies the sitemap
  rules, so noindex, canonicalized-away and empty entries stay out.
- **IndexNow key file.** With `seo.indexnow.enabled`, `/{key}.txt` returns the key. Pings aren't sent yet; they
  arrive with publishing in M3.
- **Caching.** All of these are cached and purged like pages (see [Page cache](#page-cache)).

## Page data

Every page template receives `*PageData`. Only fields marked for a page kind are set; guard optional ones with `with`
or `if`.

| Field | Type | Set for |
|---|---|---|
| `.Kind` | string | Always: `home front_page posts_page single page category tag author date search 404 410` |
| `.Site` | `Title Tagline URL Language` | Always. `URL` is the absolute base URL without a trailing slash. |
| `.Request` | `Path URL Query Paged NoIndex` | Always. `Paged` is 1 on the first page. On cached kinds `Query` holds only `q`, `page` and `cpage` (see Page cache). |
| `.Post` | `*Post` | `single`, `page`, `front_page`, `posts_page` |
| `.Posts` | `[]*Post` | `home`, `posts_page`, archives, `search` |
| `.Term` | `*Term` | `category`, `tag` |
| `.Author` | `*Author` | `author` |
| `.Date` | `Year Month Start YearURL` | `date`. `Month` is 0 on a year archive. |
| `.Search` | `Query Total` | `search` |
| `.Pagination` | `Current Total Prev Next Links` | Listing kinds |
| `.FeedURL` | string | `home`, `posts_page`, category, tag and author archives |
| `.Head` | `Title Description Canonical Robots Tags` | Always on the live site. The resolved SEO values that `head` prints (see below). |
| `.Comments` | `*Comments` | `single`, `page`, `front_page` while `discussion.comments_enabled` is on (see [Comments](#comments)) |

- **`Post`.**
  - Identity and text: `ID WPID Type Slug Title Excerpt ContentHTML ContentText Template Sticky CommentStatus`.
  - Dates and links: `PublishedAt UpdatedAt URL FeedGUID`.
  - Teaser, on listings: `Teaser TeaserText HasMore`.
  - Relations: `Authors Categories Tags Primary Ancestors FeaturedMedia`.
  - `Breadcrumb`: the SEO breadcrumb title, empty when unset.
  - Search only: `Snippet`, the matched text with `<b>` highlights.
- **`Term`:** `ID WPID Taxonomy Slug Name Description Path Ancestors URL Breadcrumb`.
- **`Author`:** `ID WPID Slug Name Bio Website Social Avatar URL Key Breadcrumb`. `Key` is the stable identity
  (legacy key, else `afsc:author:{id}`) behind the author's JSON-LD `@id`.
- **`Media`:** `ID WPID Filename Version Mime Width Height Alt Caption URL`. `URL` is the absolute URL of the
  original; use `image` or `image_url` for sized variants.

`WPID` is the WordPress id from an imported `wp:post:N`, `wp:term:N` or `wp:user:N` key, and the AFSC id otherwise.
The class funcs use it, so CSS written for WordPress ids keeps working.

## Template functions

### head

`head .` prints everything a page needs inside `<head>` except `<meta charset>` and the viewport. Call it once, in
`base.html`. It outputs, in order:

- `<title>`
- `meta description` (omitted when empty), `meta robots`, `link rel=canonical`, and `link rel=prev/next` on paginated
  listings
- Open Graph (`og:locale og:type og:title og:description og:url og:site_name og:image` with width, height and type;
  `article:published_time article:modified_time article:author` on posts)
- Twitter (`twitter:card`, `twitter:site`, and title, description and image only when an editor overrode them)
- one `<script type="application/ld+json">` with the schema.org `@graph`
- the theme's `assets` from theme.json: `.css` as stylesheets, `.js` as scripts with `defer`/`async`
- RSS `link rel=alternate` for the site feed, plus the archive feed on category, tag and author archives
- the admin's head snippet (`snippets.head_html`), unchanged

Add your own tags after it. Don't print another `<title>` or robots tag.

```html
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{head .}}
<link rel="preconnect" href="https://fonts.example.com">
</head>
```

**How values are chosen (§8.1).**

| Value | Order |
|---|---|
| Title | entity SEO title → `seo.title_templates[type]` → built-in template → site title |
| Description | entity SEO description → `seo.description_templates[type]` → empty. With `seo.auto_description` on: the excerpt (posts), term description or author bio, cut to 156 characters. |
| Canonical | entity canonical (absolute, or root-relative) → the page's own URL. Paginated archives point at themselves. None on search and 404 pages. |
| Robots | entity noindex/nofollow → type default (`seo.tag_noindex` for tags) → `index, follow, max-snippet:-1, max-image-preview:large, max-video-preview:-1`. An entity's `robots_advanced` list replaces the `max-*` part. Search, 404 and 410 pages are always `noindex, follow`. |
| OG/Twitter image | entity override → featured image → first image in the content → `seo.default_image_media`. The original file is used, as Yoast does. |

`type` is `post`, `page` (pages, the static front page and the posts page), `category`, `tag`, `author`, `home`,
`search`, `404` (also 410) or `date`. Templates use Yoast variables; see `docs/import-format.md`.

Empty category, tag and author archives return 404.

`.Head` holds the resolved `Title`, `Description`, `Canonical` and `Robots` if a template needs them, for example
for share links. Under `afsc theme check` the fixtures get a `.Head` built from default settings.

**JSON-LD.** The graph has stable `@id`s, so other nodes and later tooling can reference them:

| Node | `@id` | On |
|---|---|---|
| `Organization` (or `Person`, by `seo.knowledge_graph.type`) | `{site}/#organization` | every page |
| `WebSite` with a `SearchAction` | `{site}/#website` | every page |
| `WebPage`, `CollectionPage`, `ProfilePage` or `SearchResultsPage` | the canonical URL | every page but 404 |
| `ImageObject` | `{url}#primaryimage` | posts and pages with an image |
| `BreadcrumbList` | `{url}#breadcrumb` | pages with more than the Home crumb |
| `BlogPosting` | `{url}#article` | posts |
| `Person` | `{site}/#/schema/person/{hash}` | post authors and author archives |
| `VideoObject` | `{url}#video-{n}` | posts and pages with videos that have a name, thumbnail and upload date |

### body_class

`body_class .` returns WordPress body classes for the page:

- `home`, `blog`
- `single single-post postid-{id} single-format-standard`
- `page page-id-{id} page-template-default` or `page-template page-template-{t}`; child pages add `page-child parent-pageid-{id}`
- `archive category category-{slug} category-{id}`, and the same pattern for `tag` and `author`
- `archive date`
- `search search-results` or `search search-no-results`
- `error404`
- `paged paged-{n}` on later pages

```html
<body class="{{body_class .}}">
```

### post_class

`post_class .Post` returns
`post-{id} {type} type-{type} status-publish [format-standard] [has-post-thumbnail] [sticky] hentry category-{slug}… tag-{slug}…`.

```html
<article class="{{post_class .}}">
```

### url

`url x` gives the absolute URL of a post, term, author or media item. A root-relative path gets the site URL in front.

```html
<a href="{{url .Post}}">{{.Post.Title}}</a>
<a href="{{url "/"}}" rel="home">{{.Site.Title}}</a>
```

### asset

`asset "path"` gives the content-hashed URL of a file under `assets/`. It is served with
`Cache-Control: immutable` for a year. A missing file is an error.

```html
<link rel="stylesheet" href="{{asset "style.css"}}">
```

### menu

`menu . "location"` gives the items of the menu assigned to a location. The location must be declared in theme.json.

- **Item fields:** `Label`, `URL`, `NewTab`, `Classes`, `Kind`, `Children`, `Current`, `CurrentAncestor`.
- **`.Class`** gives WordPress classes: `menu-item menu-item-type-{kind} menu-item-{id} menu-item-has-children
  current-menu-item current-menu-ancestor`, plus the item's own classes.
- **Hidden items.** Items pointing at unpublished content are left out.

```html
{{define "menu-items"}}{{range .}}
  <li class="{{.Class}}"><a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Label}}</a>
  {{with .Children}}<ul class="sub-menu">{{template "menu-items" .}}</ul>{{end}}</li>
{{end}}{{end}}
{{with menu . "primary"}}<ul class="menu">{{template "menu-items" .}}</ul>{{end}}
```

### pagination

`pagination .` gives numbered links with WordPress `paginate_links` markup. That covers `page-numbers`,
`current` with `aria-current`, `prev`, `next` and `dots`, with one end page and two pages either side. It is empty when
there is only one page. For custom markup, use `.Pagination.Links` (`N URL Current Dots`) with `.Pagination.Prev` and
`.Pagination.Next`.

```html
{{with pagination .}}<nav class="navigation pagination"><div class="nav-links">{{.}}</div></nav>{{end}}
```

### breadcrumbs

`breadcrumbs .` gives a list of crumbs, each with a `Name` and a `URL`. The first crumb is Home, and the last crumb (the
current page) has an empty `URL`.

- **Posts** go through their primary category and its parents. Without a primary category, the oldest category
  (lowest id) is used. `%%primary_category%%` and `{category}` permalinks use the same choice.
- **Pages** go through their parent pages.
- **Date archives** show the year, then the month.
- **Breadcrumb titles.** A post, page, term or author with an SEO `breadcrumb_title` shows that instead of its name,
  wherever it appears in the trail.

The JSON-LD `BreadcrumbList` is built from the same crumbs.

```html
<ol>{{range breadcrumbs .}}<li>{{if .URL}}<a href="{{.URL}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}</li>{{end}}</ol>
```

### excerpt

`excerpt .Post [words]` returns the explicit excerpt, else the text before `readMore`, else the first 55 words (or
`words`) followed by `excerpt.more`. It returns plain text.

```html
<p>{{excerpt .}}</p>  <p>{{excerpt . 20}}</p>
```

### teaser_html

`teaser_html .Post` gives the HTML before `readMore`, or the full content when there's none. Use `.HasMore` to add a
link.

```html
{{teaser_html .}}{{if .HasMore}}<a href="{{url .}}#more-{{.WPID}}">{{t "read_more"}}</a>{{end}}
```

### reading_time

`reading_time .Post` gives minutes at 225 words per minute, at least 1.

```html
{{reading_time .Post}} min read
```

### date

`date t [layout]` formats a time in the site timezone. It uses the site date format (a Go layout) unless you pass one.

```html
<time datetime="{{date .PublishedAt "2006-01-02T15:04:05Z07:00"}}">{{date .PublishedAt}}</time>
```

### setting

`setting "group.key"` gives a site setting by its JSON path. An unknown path is an error.

```html
{{setting "site.tagline"}}  {{if setting "discussion.comments_enabled"}}…{{end}}
```

### footer

`footer .` prints the admin's body-end snippet (`snippets.body_end_html`), unchanged. Call it once, right before
`</body>`.

```html
{{footer .}}
</body>
```

### theme_setting

`theme_setting "key"` gives a theme option: the value an admin saved for this theme (by theme.json `name`), else
the theme.json default. An undeclared key is an error.

```html
{{if theme_setting "show_reading_time"}}…{{end}}
```

### t

`t "key"` gives a static string from theme.json `strings`. There is no i18n system. These keys have built-in defaults
you can override: `pagination.prev`, `pagination.next`, `breadcrumbs.home`, `breadcrumbs.search`, `breadcrumbs.404`,
`excerpt.more`. Any other missing key is an error.

```html
<a class="skip-link" href="#content">{{t "skip"}}</a>
```

### image

`image media "size" ["sizes"]` gives an `<img>` for a media item at one of the theme's `image_sizes`, or `"full"`
for the original. It sets `src`, `alt`, `width` and `height`, plus `srcset` and `sizes` when there's more than one
width to offer (see [Images](#images)). Its class is `attachment-{size} size-{size}`. The optional third argument
replaces the default `sizes` value. A nil media item gives nothing, and an undeclared size is an error.

```html
{{with .Post.FeaturedMedia}}<figure class="post-thumbnail">{{image . "large" "(max-width: 42rem) 100vw, 42rem"}}</figure>{{end}}
```

### image_url

`image_url media ["size"]` gives the absolute URL of a media item at a size, or of the original when no size is
given. Use it where you need a URL rather than an `<img>`, for example a CSS background.

```html
<div class="hero" style="background-image: url('{{image_url .Post.FeaturedMedia "large"}}')"></div>
```

### avatar

`avatar author "size"` gives the author's avatar as an `<img class="avatar">` at a theme image size, with the
author's name as `alt`. An author without an avatar gets an inline initials SVG (`class="avatar avatar-initials"`,
the size's width square, 96 px for `"full"`), coloured from a hash of the name. A nil author gives nothing, and an
undeclared size is an error.

```html
{{range .Post.Authors}}<a href="{{url .}}">{{avatar . "thumbnail"}} {{.Name}}</a>{{end}}
```

`avatar comment "size"` gives the initials SVG for a commenter. Commenters never get an image (no Gravatar).

Not yet available: `related_posts` and `share_links` arrive with M5.

## Comments

`.Comments` holds the comment section. The default theme's `parts/comments.html` and `parts/comment.html` are a
complete example.

| Field | Notes |
|---|---|
| `Open` | New comments are accepted: comments are on, the post's `comment_status` is `open`, and it was published within `discussion.close_after_days` (0 = never closes). |
| `Total` | Approved comments on the post. |
| `Threads` | `[]*Comment`, the top-level comments of the current comment page, oldest first, replies in `Children`. |
| `Pagination` | `Current Total Links` over top-level threads (`discussion.comments_per_page`, 0 = one page). Links use `?cpage=N#comments`. Nil when everything fits. |
| `Form` | `*CommentForm`, nil when closed. |
| `Notice` | The visitor's own comment awaiting moderation, right after they post it. |

- **`Comment`:** `ID Anchor AuthorName AuthorURL BodyHTML Date Depth Children ReplyURL Pending`.
  - Give the element `id="{{.Anchor}}"`. That's where the post-submit redirect lands. Imported comments keep
    their WordPress id (`comment-999`).
  - `BodyHTML` is the text escaped, with line breaks as `<br>` and http(s) URLs linked
    `rel="nofollow ugc noopener"`. Print it as is.
  - Link `AuthorURL` with `rel="nofollow ugc noopener"`. The commenter's email is never available to templates.
  - `Depth` starts at 1. Replies past `discussion.thread_depth` are listed after their parent at the deepest
    level.
  - `ReplyURL` is empty at the deepest level and when comments are closed.
- **`CommentForm`:** `Action PostID Token Honeypot ReplyTo CancelURL`. The form posts to `Action` with method
  `post` and these fields:
  - `post_id`, `token` and `parent_id` (`ReplyTo.ID`, or 0) as hidden inputs.
  - `author_name` and `author_email` (both required), `author_url` (optional) and `body` (2–5000 characters).
  - An empty text input named `{{.Honeypot}}`, visually hidden with CSS (not `type="hidden"`), with
    `tabindex="-1"` and `autocomplete="off"`. Anything typed into it marks the comment as spam.
- **Replies without JavaScript.** `ReplyURL` is `?replytocom=ID#respond`. That request skips the page cache and
  fills `Form.ReplyTo`. Mark reply links `rel="nofollow"`.
- **After posting.** `POST /comments` answers with a 303 to the comment's anchor. A comment that isn't approved
  yet comes back as `.Notice` for ten minutes through the `afsc_comment` cookie, which also skips the page cache.
  Errors get a plain, unthemed page with a link back.

## Images

Every media URL has the form `/m/{id}/{v}/{variant}/{filename}`:

- **`{v}`** is the first 8 hex characters of the file's sha256. Replacing a file changes `{v}`, so these URLs are
  cached for a year (`Cache-Control: public, max-age=31536000, immutable`). A request with an old `{v}` or filename
  gets a 301 to the current URL.
- **`{variant}`** is one of:

  | Variant | Meaning | Allowed when |
  |---|---|---|
  | `orig` | Full size | Always |
  | `w{W}` | Scaled to `W` pixels wide | `W` is in `srcset_widths`, or is the width an uncropped `image_sizes` entry gives this image |
  | `c{W}x{H}` | Cropped to exactly `W`×`H` (focal point, else centre) | A cropped `image_sizes` entry is exactly `W`×`H` |

  Any other variant is a 404, so nobody can make the server produce arbitrary sizes. Images are never enlarged.
  Changing `image_sizes` or `srcset_widths` makes URLs that are no longer allowed 404, so re-render stored content
  afterwards (see below).
- **Formats.** JPEG, PNG, GIF, WebP and AVIF go through imgproxy, `orig` included, so metadata such as EXIF is always
  stripped. The format follows the browser's `Accept` header: AVIF, then WebP, then the original format. GIFs only
  become WebP, so animation survives. These responses carry `Vary: Accept`.
- **Other files** (PDF, video, audio, SVG) are streamed unchanged from storage and support `Range` requests. They have
  no sized variants. SVGs are sent with `Content-Security-Policy: sandbox`, `Content-Disposition: attachment` and
  `nosniff`, because media shares the site's origin.

**srcset and sizes.** Content images that reference the media library, and the `image` func, work out these
attributes the same way:

- **`src`** is the variant for the image's size. `full`, an unknown size, or a size the image is too small for gives
  `orig`.
- **`width` and `height`** are the variant's dimensions, unless the content sets its own.
- **`srcset`** lists every `srcset_widths` entry narrower than the original, the variant's own width, and the
  original at its full width. Cropped sizes get no `srcset`, as in WordPress.
- **`sizes`** defaults to WordPress's `(max-width: {width}px) 100vw, {width}px`.
- **Feeds** get an absolute `src` and no `srcset`.

```html
<img src="/m/7/ab12cd34/w1024/chart.jpg" alt="…" width="1024" height="576" class="wp-image-456 size-large"
     srcset="/m/7/ab12cd34/w320/chart.jpg 320w, …, /m/7/ab12cd34/orig/chart.jpg 1600w"
     sizes="(max-width: 1024px) 100vw, 1024px">
```

**Legacy upload paths.** Old WordPress URLs such as `/wp-content/uploads/2020/05/chart-300x169.jpg` that the import
recorded keep working on the same URL with a 200, never a redirect, because email clients often don't follow
redirects for images.

- **Originals** are byte-identical to the uploaded file.
- **Resized copies** are produced by imgproxy at the recorded size and crop.
- **Format.** The response always has the format of the path's extension, with no `Accept` negotiation.
- **Caching.** These URLs are cached for a day (`public, max-age=86400`), with `ETag` and `Last-Modified`.
- **Hits** are counted in memory and added to `media_legacy_paths.hits` every 60 seconds.

## Content node overrides

`content.Render` is the only code that turns content into HTML. A theme can replace one node type's markup with
`templates/nodes/<node>.html`.

- **Node names.** Any block node from the schema can be overridden: `paragraph`, `heading`, `blockquote`, `bulletList`,
  `orderedList`, `listItem`, `codeBlock`, `horizontalRule`, `image`, `figure`, `figcaption`, `gallery`, `embed`,
  `video`, `table`, `tableRow`, `tableCell`, `tableHeader`, `details`, `detailsSummary`, `detailsContent`,
  `container`, `readMore`, `rawHtml`.
- **What the partial receives** (`NodeData`):
  - `.Type`, and `.Attrs`, the validated attributes. Optional keys may be absent, so use `index`.
  - `.Children`: the rendered children, with their own overrides applied.
  - `.Default`: the built-in markup for the whole node, so you can wrap it instead of rewriting it.
  - `.Media` and `.Src` for `image` and `video` nodes that reference the media library. `.Src` is the original's
    URL; `.Default` already has the sized `src`, `srcset` and `sizes`.
- **Plain text** (search and excerpts) always comes from the built-in renderer.
- **Images inside a `figure` or `gallery`** use the parent's built-in markup. Override `figure` or `gallery` to change
  them.
- **Feeds** always use the built-in markup with absolute URLs.

```html
<!-- templates/nodes/blockquote.html -->
<blockquote class="pull-quote {{index .Attrs "classes"}}">{{.Children}}</blockquote>

<!-- templates/nodes/image.html -->
<div class="frame">{{.Default}}</div>
```

- **When overrides apply.** Post HTML is rendered when it is imported or saved, and stored. After changing `nodes/`,
  `image_sizes` or `srcset_widths`, re-render the stored HTML:

  ```
  afsc theme check --rerender themes/mytheme
  ```

  In dev mode, pages render content on every request, so partial edits show at once.
- **What to avoid in node partials.** They run at save time, so avoid `menu`, `setting` and `date` in them. Those see
  default settings outside a request.

## Page cache

AFSC keeps rendered pages in memory and sends cache headers so a CDN can hold them too. The TTL is the
`cache.page_ttl_seconds` setting (default 60 seconds).

| Response | Cached in memory | `Cache-Control` |
|---|---|---|
| 200 for `home`, `front_page`, `posts_page`, `single`, `page`, `category`, `tag`, `author`, `date`, feeds, sitemaps, `robots.txt`, `llms.txt` and the IndexNow key file | Yes | `public, max-age=T, s-maxage=T, stale-while-revalidate=T`, plus a strong `ETag` (`304` on `If-None-Match`) |
| 404, 410 and redirects | No | The same public value, without an `ETag` |
| `search` | No | `private, no-store` |
| Theme assets, `/m/` media and legacy upload paths | No | Their own headers (above) |

With a TTL of 0 the header is `no-cache` and nothing is kept in memory. Responses carry `X-Cache: HIT` or `MISS`.

What this means for templates:

- **One response for every visitor.** The cache key is the path plus the `q`, `page` and `cpage` query parameters. Don't make
  output depend on cookies, request headers, the time of day or other query parameters. Those vary per request, and
  the first visitor's version would be served to everyone.
- **`.Request.Query`** on cached kinds contains only `q`, `page` and `cpage`, so tracking parameters such as `utm_source` never
  end up in a stored page.
- **Requests that skip the cache:** an `Authorization` header, any cookie named `afsc_*` (reserved for previews and
  commenters), a `preview` or `replytocom` parameter, and the WordPress-style `/?p=` parameters handled by the
  compatibility layer. A page showing the visitor's own pending comment or a reply form is sent `private, no-store`.
- **Comments.** Approving a comment, or unapproving one, purges the post's page with every `cpage`. Posting a
  comment never writes to the cache.
- **Purging.** Changing a post purges its URL, the home page, the posts page and its date, term and author archives,
  each with every page number and its feed, plus the sitemaps that list it and the sitemap index. Settings, menus, imports and `--rerender` purge everything. In dev mode,
  a theme reload purges everything.

## Content Security Policy

Every public page carries a `Content-Security-Policy` header built from:

- core defaults: `'self'` for everything, `style-src 'unsafe-inline'`, `data:` images and fonts, `object-src 'none'`,
  `frame-ancestors 'self'`, and `frame-src` for the embed providers content renders (YouTube nocookie, Vimeo,
  Spotify, SoundCloud, Google Maps)
- theme.json `csp` origins
- the admin's extra origins (`security.extra_csp_hosts`)
- a `'sha256-…'` hash of every inline `<script>` in the templates, node partials and the admin's snippets

Hashes are computed when the theme, settings or snippets load, never per request, so cached pages and 304s keep a
matching policy. Nonces aren't used for that reason. What this means for themes:

- **Inline scripts must be static.** The hash is taken from the template source, so a `{{…}}` action inside an inline
  script breaks it. Put dynamic values in `data-` attributes or a JSON block (`type="application/json"`), or ship the
  script as an asset.
- **No inline event handlers** (`onclick=` and friends); they are never allowed.
- Third-party scripts, fonts, frames and API hosts must be listed in `csp`.

With `security.csp_report_only` on, the header is `Content-Security-Policy-Report-Only` instead. Pages also get
`X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin` and, when `security.hsts` is on
and the base URL is https, `Strict-Transport-Security: max-age=31536000`. Media under `/m/` keeps its own headers.

## afsc theme check

```
afsc theme check <dir> [--rerender]
```

The command loads the theme, which fails on JSON errors, parse errors or unknown funcs. It then renders:

- every page template against each page kind it can serve, once with every optional field set and once with them all
  empty;
- every node partial against sample content.

It fails on a missing field, a missing map key, a missing `t` string, an undeclared menu location or theme setting, an
unguarded nil, or a template file that isn't part of the hierarchy (shared markup belongs in `parts/`).

`--rerender` then re-renders every post's stored HTML with the theme's node partials (needs `AFSC_DATABASE_URL`). When
any post changed, it tells every running server to purge its page cache.
