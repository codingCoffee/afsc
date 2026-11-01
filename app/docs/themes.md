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
  "csp": {"script": [], "style": [], "frame": [], "img": [], "connect": []},
  "settings": {"show_reading_time": {"type": "bool", "label": "Show reading time", "default": true}},
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
| `csp` | Extra hosts for the Content Security Policy (§7.3). |
| `settings` | Theme options with defaults. Admins edit them from M3; until then `theme_setting` returns the default. |
| `strings` | Static strings for `t`. They override the built-in defaults listed under `t`. |
| `posts_per_page` | Per-archive overrides of the site's posts-per-page: `home` (also the posts page), `category`, `tag`, `author`, `date`, `search`. 1–100. |

## Layout: base.html and blocks

Without `base.html`, every page template is a complete HTML document.

With `base.html`, every page is rendered through it. Page templates only `define` the blocks the layout declares:

```html
<!-- base.html -->
<!doctype html>
<html lang="{{.Site.Language}}">
<head><title>{{block "title" .}}{{.Site.Title}}{{end}}</title></head>
<body class="{{body_class .}}">{{block "content" .}}{{end}}</body>
</html>

<!-- single.html -->
{{define "title"}}{{.Post.Title}} | {{.Site.Title}}{{end}}
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

- **Precedence.** On the same path, date archives win, then category, tag and author bases, then pages, then posts.
- **Pagination.** `page/1/` redirects to the archive. A page past the last one is a 404.
- **Feed aliases.** `/feed/rss2/` and `/feed/atom/` redirect to `/feed/`. `{post}/feed/` redirects to the post.
- **Canonical URLs.**
  - Uppercase paths, duplicate slashes, an `/index.php` prefix and the wrong trailing slash all get a 301 to the
    canonical form. File-like paths such as `/robots.txt` keep no trailing slash.
  - A post whose URL tokens don't match (a wrong date or category) also gets a 301 to its canonical URL.
- **Visibility.** Only `published` posts whose publish time has passed are shown. Drafts, scheduled, private and
  trashed posts are 404s.
- **Request order.**
  1. Theme assets, then `/m/` media URLs. A `/m/` URL naming no media item, or a variant the theme doesn't
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
  distinct paths.

## Page data

Every page template receives `*PageData`. Only fields marked for a page kind are set; guard optional ones with `with`
or `if`.

| Field | Type | Set for |
|---|---|---|
| `.Kind` | string | Always: `home front_page posts_page single page category tag author date search 404 410` |
| `.Site` | `Title Tagline URL Language` | Always. `URL` is the absolute base URL without a trailing slash. |
| `.Request` | `Path URL Query Paged NoIndex` | Always. `Paged` is 1 on the first page. |
| `.Post` | `*Post` | `single`, `page`, `front_page`, `posts_page` |
| `.Posts` | `[]*Post` | `home`, `posts_page`, archives, `search` |
| `.Term` | `*Term` | `category`, `tag` |
| `.Author` | `*Author` | `author` |
| `.Date` | `Year Month Start YearURL` | `date`. `Month` is 0 on a year archive. |
| `.Search` | `Query Total` | `search` |
| `.Pagination` | `Current Total Prev Next Links` | Listing kinds |
| `.FeedURL` | string | `home`, `posts_page`, category, tag and author archives |

- **`Post`.**
  - Identity and text: `ID WPID Type Slug Title Excerpt ContentHTML ContentText Template Sticky CommentStatus`.
  - Dates and links: `PublishedAt UpdatedAt URL FeedGUID`.
  - Teaser, on listings: `Teaser TeaserText HasMore`.
  - Relations: `Authors Categories Tags Primary Ancestors FeaturedMedia`.
  - Search only: `Snippet`, the matched text with `<b>` highlights.
- **`Term`:** `ID WPID Taxonomy Slug Name Description Path Ancestors URL`.
- **`Author`:** `ID WPID Slug Name Bio Website Social Avatar URL`.
- **`Media`:** `ID WPID Filename Version Mime Width Height Alt Caption URL`. `URL` is the absolute URL of the
  original; use `image` or `image_url` for sized variants.

`WPID` is the WordPress id from an imported `wp:post:N`, `wp:term:N` or `wp:user:N` key, and the AFSC id otherwise.
The class funcs use it, so CSS written for WordPress ids keeps working.

## Template functions

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

- **Posts** go through their primary category and its parents.
- **Pages** go through their parent pages.
- **Date archives** show the year, then the month.

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

### theme_setting

`theme_setting "key"` gives a theme option from theme.json. An undeclared key is an error.

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

Not yet available: `head` and `footer` arrive with the SEO milestone (M2), `avatar` with M3, and `related_posts` and
`share_links` with M5. Until `head` lands, themes write their own `<title>`, stylesheet links and `noindex` meta, as
the default theme does.

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

`--rerender` then re-renders every post's stored HTML with the theme's node partials (needs `AFSC_DATABASE_URL`).
