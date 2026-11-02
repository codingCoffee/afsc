# decisions.json

The per-site decision table (migration plan §3.4). `inventory` writes one item per thing it found with
`status: "proposed"`. A human sets `decision` and `status: "confirmed"` on every item. `transform` fails closed: it
refuses to run while any item is unconfirmed or invalid, or when the data contains an item the table has never seen
(re-run `inventory`; confirmed decisions are kept across runs).

```json
{
  "version": 1,
  "items": [
    {"id": "shortcode:pullquote", "kind": "shortcode", "name": "pullquote", "count": 12,
     "samples": ["wp:post:123"], "allowed": ["render_php", "drop", "keep_text"],
     "proposed": "render_php", "decision": "render_php",
     "options": {"include": ["/wp-code/acme/inc/shortcodes.php"]},
     "status": "confirmed", "note": "registered in acme/inc/shortcodes.php"}
  ]
}
```

- `allowed` narrows the kind's values where needed (for example, `convert` only for shortcodes with a converter).
- Items whose subject disappeared from the data are kept with `"stale": true` and ignored.
- `options` carries values some decisions need (listed below).

## Kinds

| Kind | Name | Values | Meaning |
|---|---|---|---|
| `shortcode` | tag | `convert` | Use afsc-wp's converter: `caption`/`wp_caption` → figure, `gallery` → gallery (no `ids` = attached images in `menu_order`), `embed` and Jetpack `youtube`, `vimeo`, `tweet`, `instagram`, `spotify`, `soundcloud`, `googlemaps` → embed, `video` → video, `audio` → sanitized `<audio>` rawHtml. |
| | | `render_php` | Render that one shortcode with `wp-filter.php --render-shortcode`, sanitize with the trusted policy and store as flagged rawHtml. `options.include`: PHP files (container paths) that register it. |
| | | `drop` | Remove the tags. Enclosing shortcodes keep their inner content, so no text is lost. |
| | | `keep_text` | Not a shortcode: leave the text as is (`[sic]`, `[1]`). |
| `block` | block name | `convert` | Walk its saved HTML into nodes (`core/*` default; `core/image`, `core/gallery`, `core/embed`, `core/html`, `core/more`, `core/nextpage` have dedicated handling). |
| | | `raw_html` | Keep its saved HTML as sanitized rawHtml. Dynamic blocks have no saved HTML and are flagged `dynamic_block`. |
| `jetpack_module` | module | `builtin` | AFSC has it (share links, related posts, lightbox, lazy images). |
| | | `replicate_theme` | Reproduce in the theme (tiled galleries, widgets). |
| | | `drop` | Not needed (Photon is replaced by rewritten media URLs). |
| `theme_hook` | `hook:callback` | `replicate_theme`, `drop`, `needs_afsc` | A `the_content`/`the_excerpt`/`wp_head`/`wp_footer`/… callback in theme or mu-plugin code. It is never baked into content. |
| `plugin` | plugin directory | `replicate_theme`, `drop`, `needs_afsc` | An active plugin without an adapter. Nothing is converted from it. |
| `iframe_host` | host | `embed` | Map to an AFSC embed (known providers only). |
| | | `raw_html` | Keep as sanitized rawHtml. The trusted policy only allows iframes from its own host list. |
| | | `drop` | Remove. |
| `element` | element name | `raw_html`, `drop` | `script`, `style`, `form`, `object`… The sanitizer removes script, style and form contents even under `raw_html`; any visible text lost is flagged `text_loss`. |
| `taxonomy` | taxonomy | `drop`, `category`, `tag` | Taxonomies other than `category`, `post_tag` and `nav_menu`. |
| `post_type` | post type | `skip`, `post`, `page` | Custom post types with published items. Skipped items' URLs need a redirect or `verify.approved_404`. |
| `marker` | `nextpage` | `merge` | Pages merge into one post; `/post/2/`, `/post/3/`… 301 to the post. |
| `empty_paragraphs` | `empty_paragraphs` | `drop`, `keep` | `<p>&nbsp;</p>` spacing. Keep only if it matters visually. |
| `yoast_variable` | variable | `keep` | Leave it; AFSC renders unknown variables as nothing. |
| | | `remove` | Delete it from templates and per-entity values. |
| | | `replace` | Replace with `options.with`. |
| | | `expand` | Fill in per post at migration time: `focuskw`, `id`, `parent_title`, `post_year`, `post_month`, `post_day`. |
| `permalink_structure` | the structure | `keep` | Only valid when AFSC can express it. |
| | | `change` | Use `options.pattern` (AFSC tokens); every post gets a 301 from its WordPress URL. Needed for `%author%`, `%hour%`, `%post_id%`, plain and `index.php` permalinks. |
| `category_base` | `category` or `tag` | `keep`, `change` | When the base isn't one path segment; `change` uses `options.base` and adds 301s. |
| `redirect_regex` | id (sha1 of the origin) | `rewrite` | Use `options.source` (RE2) instead of the PCRE origin. |
| | | `drop` | Don't migrate it (lookarounds and backreferences can't be expressed). |
| `missing_media` | uploads-relative path | `keep_src` | Content keeps pointing at the old absolute URL. A cutover blocker until decided. |
| | | `remove` | Remove the image from content. |
| `password_protected` | `all` | `private` | AFSC has no password-protected posts; they import as private. |
| `oembed` | host | `link` | A bare URL from a provider AFSC can't embed stays a link. |
| | | `raw_html` | Use WordPress's cached oEmbed HTML (`_oembed_*`), sanitized. |

`docs/decisions.md` must match `internal/decisions.Kinds`; a new kind needs both.
