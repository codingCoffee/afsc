# Plugins

AFSC plugins are Go packages compiled into the binary (build plan §12). There's no runtime loading: a plugin is
trusted code with the same access as the rest of AFSC. An admin switches plugins on and off, and sets their options,
under **Admin → Settings → Plugins**.

- [Adding a plugin](#adding-a-plugin)
- [The Plugin interface](#the-plugin-interface)
- [Host API](#host-api)
- [Filters](#filters)
- [comment.check](#commentcheck)
- [Actions](#actions)
- [Rules for plugin output](#rules-for-plugin-output)
- [Settings](#settings)
- [Template funcs](#template-funcs)
- [Public routes and assets](#public-routes-and-assets)
- [Admin API routes](#admin-api-routes)
- [Jobs and schedules](#jobs-and-schedules)
- [Migrations](#migrations)
- [Testing a plugin](#testing-a-plugin)
- [Example: reading progress bar](#example-reading-progress-bar)
- [Built-in plugins](#built-in-plugins)

## Adding a plugin

1. Write a package under `plugins/<id>/` that implements `plugins.Plugin` (from `afsc/internal/plugins`).
2. Add it to the list in `plugins/registry.go`.
3. Build: `go build .` from `app/`. Run `afsc migrate up` if it has migrations.

Registry order matters only as the tie-break between hooks with the same priority.

The ID matches `^[a-z][a-z0-9]{1,31}$`. It names everything the plugin owns: template funcs (`plg_<id>_…`),
URLs (`/_plugins/<id>/…`, `/admin/api/v1/plugins/<id>/…`), job kinds (`<id>.…`) and tables (`plg_<id>_…`).

## The Plugin interface

```go
type Plugin interface {
	ID() string
	Init(ctx context.Context, h *plugins.Host) error
}

// optional
type Describer interface {
	Info() plugins.Info // Name, Description, DefaultEnabled, AlwaysOn
}
```

- `Init` runs once per process: in `afsc serve`, `afsc worker`, and also in `afsc theme check`, `afsc import` and
  `afsc migrate`. It must only register things. It must not open connections, read files outside its embedded
  assets, or start goroutines.
- Everything registered is validated at startup, and the process refuses to start on any problem:
  - duplicate names
  - func names without the prefix
  - routes outside the plugin's prefix
  - migrations touching other tables
  - invalid setting defaults
- A plugin without `Info` is named by its ID and is on by default. `AlwaysOn` plugins can't be switched off.

**Enabled state** is read from the settings snapshot on every call. When a plugin is off:

| What | Behaviour |
|---|---|
| filters | skipped |
| template funcs | stay registered, so templates still parse, but return zero values (`""`, `nil`, `false`) |
| public routes, assets and admin routes | 404 |
| jobs | do nothing |
| migrations | still run |

Switching a plugin on or off is a settings save, which purges the whole page cache.

## Host API

| Method | What it does |
|---|---|
| `plugins.AddFilter(h, hook, prio, fn)` | Run `fn` on a [filter](#filters). Lower `prio` runs first. |
| `plugins.AddAction(h, event, prio, fn)` | Run `fn` in a job after a change commits ([actions](#actions)). |
| `h.RegisterTemplateFuncs(template.FuncMap)` | [Template funcs](#template-funcs) named `plg_<id>_…`. |
| `h.RegisterAssets(fs.FS)` / `h.AssetURL(name)` | Static files with content-hashed, immutable URLs. |
| `h.RegisterPublicRoute(method, subpath, http.Handler)` | GET (and HEAD) or POST at `/_plugins/<id>/<subpath>`. |
| `h.RegisterAdminAPI(method, subpath, cap, http.Handler)` | JSON routes at `/admin/api/v1/plugins/<id>/<subpath>`. |
| `h.RegisterSettings(...plugins.Setting)` | Options shown in the admin. |
| `h.RegisterJob(kind, jobs.Handler, jobs.Options)` / `h.RegisterSchedule(jobs.Schedule)` | Background work on the job queue. |
| `plugins.AddAllActions(h, prio, fn)` | Subscribe to every event, with `Data` as raw JSON. |
| `h.Key()` | A 32-byte key for this plugin, derived from `AFSC_JWT_SECRET` (HKDF, info `afsc-<id>`). |
| `h.RegisterMigrations(fs.FS)` | golang-migrate files for `plg_<id>_…` tables. |
| `h.DB()`, `h.BaseURL()`, `h.Logf()` | The pool (nil in theme check, import and migrate), `AFSC_PUBLIC_BASE_URL`, and a logger prefixed with the ID. |
| `h.Enabled()`, `h.Settings()`, `h.Bool/String/Number(key)` | Current state and options. |

## Filters

A filter passes a value through every enabled handler in priority order. Each handler gets the value so far plus
read-only arguments, and returns the new value.

```go
plugins.AddFilter(h, plugins.BodyEnd, 10,
	func(ctx context.Context, v template.HTML, d *theme.PageData) (template.HTML, error) {
		return v + "<!-- hi -->", nil
	})
```

| Hook | Value | Args | Timeout | Runs |
|---|---|---|---|---|
| `head.tags` | `template.HTML` | `*theme.PageData` | 100 ms | In `{{ head }}`, after the title, SEO tags, theme assets and feed links, before the admin's head snippet. |
| `body.end` | `template.HTML` | `*theme.PageData` | 100 ms | In `{{ footer }}`, before the admin's body-end snippet. |
| `content.html` | `template.HTML` | `*theme.PageData` | 100 ms | On the main post of single, page and front-page renders (also previews). Not on listings, teasers or feeds. |
| `seo.meta` | `*seo.Meta` (`Title`, `Tags []seo.Tag{Kind, Key, Value}`) | `*theme.PageData` | 100 ms | Before the head tags print. `Kind` is `name`, `property` (`<meta>`) or `rel` (`<link>`). Values are escaped when printed. |
| `seo.schema_graph` | `[]map[string]any` | `*theme.PageData` | 100 ms | Before the JSON-LD graph prints. |
| `sitemap.entries` | `[]seo.SitemapURL` | `plugins.SitemapArgs{Type, Page}` | 500 ms | Per sitemap file. Added entries don't change pagination or the index. |
| `feed.item` | `*plugins.FeedItem` | `*theme.Post` | 100 ms | Per RSS item. Return nil to drop the item. |
| `csp.directives` | `map[string][]string` (directive → extra sources) | none | 100 ms | When the public CSP is rebuilt (each settings snapshot). Only `https://` origins (optionally `*.`) on `script-src style-src img-src font-src connect-src frame-src` are kept. |

**Failure isolation.**
- Each handler runs with the hook's timeout and recovers from panics. A handler that errors, panics or times out is
  skipped: the next handler gets the value it was given, so a broken plugin never breaks a page (fail-open).
- Handlers get a copy of slice, map and struct values. Mutating the input doesn't affect anything if you then fail.
- A timed-out handler keeps running in its goroutine with a cancelled `ctx`, and its result is dropped. Honour
  `ctx`.
- Failures are logged at most once a minute per plugin and hook.
- With no handlers registered, a filter costs nothing.

## comment.check

`plugins.CommentCheck` scores a public comment submission. The value is a `spam.Verdict` (`Score`, `Reasons`,
`Hard`, `ForcePending`) and the args are the `spam.Input` (name, email, URL, body, IP, honeypot, form timing). The
built-in `spam` plugin runs the §11 pipeline at priority 0, so a handler at a higher priority sees its verdict and
adds to it:

```go
plugins.AddFilter(h, plugins.CommentCheck, 10,
	func(ctx context.Context, v spam.Verdict, in spam.Input) (spam.Verdict, error) {
		if strings.Contains(in.Body, "crypto") {
			v = v.Merge(spam.Verdict{Score: 4, Reasons: []spam.Reason{{Check: "crypto", Delta: 4}}})
		}
		return v, nil
	})
```

Unlike the render filters, `comment.check` **fails closed**:
- The 2 s timeout covers the whole chain, not each handler.
- A handler that errors, panics or times out stops the chain. The comment is held as `pending`, with reason
  `{"check": "check_failed", "detail": "<plugin>: …"}` added to the reasons gathered so far.
- A hard verdict (`Hard: "spam"`) stops the chain.

Moderators see every reason, with its delta, in the comment's spam details.

## Actions

Actions tell plugins that something changed. They run off the request path:
- The change's transaction queues one `plugins.action` job per enabled subscriber. A rolled-back change fires
  nothing.
- The job runs that plugin's handlers for the event, in priority order, with a 30 s timeout each.
- An error or panic retries the plugin's handlers for that event, up to 3 attempts with the job backoff.
- Delivery is at least once. Make handlers idempotent on `ev.ID`, which stays the same across retries.

```go
plugins.AddAction(h, plugins.PostPublished, 0,
	func(ctx context.Context, ev plugins.Envelope[plugins.PostData]) error {
		return jobs.Enqueue(ctx, h.DB(), "myplugin.notify", map[string]any{"post_id": ev.Data.ID}, time.Time{})
	})
```

| Event | Fired by | `Data` |
|---|---|---|
| `post.saved` | Creating or saving a post or page, restoring one from trash | `PostData{ID, Type, Status, Title, Slug, URL, ModifiedAt}` |
| `post.published` | A save or the scheduler moving a post to published | `PostData` + `PublishedAt` |
| `post.unpublished` | A save moving a published post to another status, trashing a published post | `PostData` + `PreviousStatus` |
| `post.deleted` | Deleting a post from trash for good | `PostDeletedData{ID, Type, Slug}` |
| `term.saved` | Creating or editing a category or tag | `TermData{ID, Taxonomy, Slug, Name, URL}` |
| `comment.created` | A public comment that isn't spam, a staff reply | `CommentData{ID, PostID, PostURL, ParentID, Status, AuthorName}` |
| `comment.status_changed` | Moderation, once per changed comment; a permanent delete has `To: "deleted"` | `CommentStatusData{ID, PostID, From, To}` |
| `media.uploaded` | A new library upload (not a duplicate of an existing file) | `MediaData{ID, URL, Mime, Width, Height, Filename}` |
| `user.login` | A sign-in, first-user setup or accepted invite | `LoginData{UserID, Username, Role}` |
| `settings.changed` | A settings group saved with changes, or the spam blocklist | `SettingsData{Group, Fields}`, the changed field names |

`post.saved` comes first when a save also publishes or unpublishes. URLs are absolute. Payloads never carry
commenter emails, IPs, user agents, comment bodies or setting values. A plugin that needs more reads it from the
database or the admin API. `afsc import` fires no events.

The `webhooks` plugin sends `{"event", "occurred_at", "data"}` as the body and the envelope ID as
`X-AFSC-Event-Id`.

## Rules for plugin output

- **The page cache shares output across visitors.** Filters and funcs must depend only on the page and settings,
  never on cookies, the visitor or the time of day.
- **No inline scripts.** The public CSP allows inline scripts by hash only, and the hashes are computed from the
  theme and the admin's snippets, not from per-page output. Put JS in assets and load it with
  `<script src="…" defer>`. Inline `style="…"` attributes are allowed.
- **No third-party requests by default.** Anything loaded from another origin is blocked unless an admin adds the
  host under Settings → Security, or the plugin returns it from `csp.directives`.
- **Escape everything** you put in `template.HTML`: `template.HTMLEscapeString` for text and attributes.

## Settings

```go
one, ten := 1.0, 10.0
h.RegisterSettings(
	plugins.Setting{Key: "height", Label: "Bar height (px)", Type: "number", Default: 3, Min: &one, Max: &ten},
	plugins.Setting{Key: "placement", Label: "Placement", Type: "select", Default: "auto", Options: []string{"auto", "manual"}},
)
```

- Types are the theme setting types: `bool`, `string` (≤ 4096 bytes), `number` (with optional `Min`/`Max`),
  `select` (one of `Options`) and `color` (`#rrggbb`).
- Values are stored in the `plugins` settings group as `{enabled: {id: bool}, settings: {id: {key: value}}}` and
  validated on save.
- `h.Settings()` applies defaults and ignores stored values that no longer validate. Numbers come back as `float64`.

## Template funcs

```go
h.RegisterTemplateFuncs(template.FuncMap{"plg_progress_bar": p.bar})
```

- Names must be `plg_<id>_<name>`.
- Funcs are added to every theme's func map, including in `afsc theme check`, so themes can call them whether the
  plugin is on or not. While it's off, or if the func panics, it returns zero values.
- A func that returns a non-nil `error` fails the page render; return zero values instead for "nothing to show".

## Public routes and assets

```go
//go:embed assets
var assets embed.FS

sub, _ := fs.Sub(assets, "assets")
h.RegisterAssets(sub)
url := h.AssetURL("progress.js") // /_plugins/progress/assets/<sha12>/progress.js
```

- Assets are served with the matching `Content-Type`, `nosniff` and a one-year immutable cache. A stale hash still
  gets the current file, cached for 60 s. Each file is at most 2 MB.
- `RegisterPublicRoute("GET", "feed.json", handler)` serves `/_plugins/<id>/feed.json`. Subpaths match
  `[a-z0-9][a-z0-9._/-]*` and can't start with `assets/`.
- Responses get the public security headers (CSP, `nosniff`, referrer policy, HSTS). They're never stored in the
  page cache, so set your own `Cache-Control`.
- POST bodies are capped at 1 MB. There's no CSRF token: don't change state on behalf of a signed-in user from a
  public route.
- `/_plugins/` and `/_theme/` are reserved. Admin-made slugs can't start with `_`, and `afsc import` warns about
  top-level `_plugins` or `_theme` slugs.

## Admin API routes

```go
h.RegisterAdminAPI("GET", "items/:id", authz.ManageSettings, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	a, _ := plugins.AdminFrom(r.Context()) // a.User, a.Role, a.IP
	plugins.JSON(w, http.StatusOK, map[string]string{"id": plugins.PathParam(r, "id")})
}))
```

- Routes mount at `/admin/api/v1/plugins/<id>/<subpath>` through the same `route()` as core routes. They get Bearer
  auth, the Origin and JSON checks for non-GET requests, the capability check, and the "every route has a
  capability" test.
- A capability is required.
- Errors use `plugins.Error(w, status, field, message)`, matching core errors.
- Write an audit entry (`afsc/internal/audit`) for changes an admin would want to trace.

## Jobs and schedules

```go
h.RegisterJob("progress.cleanup", func(ctx context.Context, payload json.RawMessage) error { … }, jobs.Options{})
h.RegisterSchedule(jobs.Schedule{Kind: "progress.cleanup", Every: 24 * time.Hour})
```

- Kinds start with `<id>.`.
- `jobs.Options{Backoff}` sets the retry delay for the kind; the default doubles from 10 s up to 1 h.
- Handlers run on any `afsc serve` or `afsc worker` with jobs enabled. They retry with backoff up to 8 attempts, so
  make them idempotent. Enqueue with `jobs.Enqueue(ctx, tx, kind, payload, runAt)`, inside your transaction when the
  job belongs to a change.

## Migrations

```go
//go:embed migrations/*.sql
var migrations embed.FS

sub, _ := fs.Sub(migrations, "migrations")
h.RegisterMigrations(sub)
```

- Files are golang-migrate pairs, `0001_init.up.sql` / `0001_init.down.sql`. Every table, index, sequence and type
  they create, alter or drop must be named `plg_<id>_…`; this is checked at startup.
- Each plugin has its own version table, `plg_<id>_schema_migrations`. `afsc migrate up` applies core migrations,
  then every plugin's in registry order.
- `afsc migrate down --plugin <id> [version]` reverts one plugin. Plain `afsc migrate down` only touches core.
- Migrations run whether or not the plugin is switched on.

## Testing a plugin

`afsc/internal/plugins/plugintest` builds a runtime with no database:

```go
s := settings.Defaults()
s.Plugins.Enabled = map[string]bool{"progress": true}
rt := plugintest.Runtime(t, &s, progress.New())
body := plugins.ApplyFilter(ctx, rt, plugins.BodyEnd, "", &theme.PageData{Kind: theme.KindSingle})
```

Change `s` during the test to change what the plugin sees. `rt.ServePublic` serves its routes and assets to an
`httptest.Recorder`, and `rt.Funcs()` gives its template funcs. `plugins/progress/progress_test.go` is a complete
example.

## Example: reading progress bar

`plugins/progress` is compiled in and **off by default**. Switch it on under Settings → Plugins. It draws a bar at
the top of posts that fills as the reader scrolls.

```go
package progress

//go:embed assets
var assets embed.FS

type Plugin struct{ h *plugins.Host }

func New() *Plugin            { return &Plugin{} }
func (*Plugin) ID() string    { return "progress" }
func (*Plugin) Info() plugins.Info {
	return plugins.Info{Name: "Reading progress bar", Description: "…"} // DefaultEnabled false
}

func (p *Plugin) Init(_ context.Context, h *plugins.Host) error {
	p.h = h
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return err
	}
	h.RegisterAssets(sub) // progress.css, progress.js
	one, ten := 1.0, 10.0
	h.RegisterSettings(
		plugins.Setting{Key: "color", Label: "Bar color", Type: "color", Default: "#2271b1"},
		plugins.Setting{Key: "height", Label: "Bar height (px)", Type: "number", Default: 3, Min: &one, Max: &ten},
		plugins.Setting{Key: "placement", Label: "Placement", Type: "select", Default: "auto", Options: []string{"auto", "manual"}},
		plugins.Setting{Key: "posts_only", Label: "Posts only", Type: "bool", Default: true},
	)
	plugins.AddFilter(h, plugins.HeadTags, 10, p.head) // the stylesheet
	plugins.AddFilter(h, plugins.BodyEnd, 10, p.body)  // the bar + deferred script, when placement is auto
	h.RegisterTemplateFuncs(template.FuncMap{"plg_progress_bar": p.bar})
	return nil
}
```

- **`head`** adds `<link rel="stylesheet" href="{{AssetURL progress.css}}">` on single posts (and pages when
  `posts_only` is off).
- **`body`** adds the markup when `placement` is `auto`:

  ```html
  <div class="plg-progress" style="--plg-progress-color:#2271b1;--plg-progress-height:3px" aria-hidden="true">
    <div class="plg-progress__bar"></div></div>
  <script src="/_plugins/progress/assets/<hash>/progress.js" defer></script>
  ```

  The bar is decorative, so it's `aria-hidden`. The script is about 20 lines: a passive scroll listener that sets
  `transform: scaleX(p)` once per animation frame. The CSS turns the transition off for `prefers-reduced-motion` and
  hides the bar when printing.
- **`plg_progress_bar .`** returns the same markup when `placement` is `manual`, for themes that want to place it
  themselves, for example inside a sticky header:

  ```html
  <header class="site-header">{{ plg_progress_bar . }} …</header>
  ```

- **CSP and cache.** The CSS and JS come from `'self'` and the inline `style` is allowed, so the CSP doesn't change.
  The output depends only on the page kind and settings, so cached pages are correct for everyone.

The full source is in `plugins/progress/`, and its tests in `progress_test.go`.

## Built-in plugins

| ID | What | Default |
|---|---|---|
| `spam` | The [comment.check](#commentcheck) pipeline | always on |
| `webhooks` | Signed POSTs on every event; see [webhooks.md](webhooks.md) | on |
| `progress` | The example above | off |

Later steps of M5 add `related`, `share` and `lightbox`. `seo` and `indexnow` are
core packages, not plugins; they run the `seo.*` and `sitemap.entries` filters.
