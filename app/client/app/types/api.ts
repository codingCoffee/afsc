// JSON shapes of the admin API. docs/admin-api.md lists the routes; keep both in sync with the Go handlers.

export type PostType = 'post' | 'page'
export type PostStatus = 'draft' | 'pending' | 'scheduled' | 'published' | 'private' | 'trash'

// a ProseMirror document as validated by internal/content
export interface PMNode {
  type: string
  attrs?: Record<string, unknown>
  content?: PMNode[]
  marks?: { type: string, attrs?: Record<string, unknown> }[]
  text?: string
}

export interface AuthorRef { id: number, display_name: string, slug: string }
export interface TermRef { id: number, name: string, slug: string, taxonomy: 'category' | 'tag' }

export interface PostListItem {
  id: number
  type: PostType
  status: PostStatus
  title: string
  slug: string
  url: string | null // public URL while visible
  published_at: string | null
  updated_at: string
  authors: AuthorRef[]
  categories: TermRef[]
  tags: TermRef[]
  sticky: boolean
  version: number
  can_edit: boolean
}

export interface SeoFields {
  title: string | null
  description: string | null
  canonical: string | null
  robots_noindex: boolean | null
  robots_nofollow: boolean | null
  robots_advanced: string[] | null
  og_title: string | null
  og_description: string | null
  og_image_media_id: number | null
  twitter_title: string | null
  twitter_description: string | null
  twitter_image_media_id: number | null
  focus_keyphrase: string | null
  breadcrumb_title: string | null
  is_cornerstone: boolean
}

export interface PostInput {
  type: PostType // create only
  version?: number // required on update
  title: string
  slug: string // empty = derive from title
  status: PostStatus
  published_at: string | null // RFC 3339; future + publish = scheduled
  excerpt: string
  content: PMNode
  parent_id: number | null // pages only
  template: string | null
  featured_media_id: number | null
  author_ids: number[] // ordered; empty = the caller's own author
  category_ids: number[]
  primary_category_id: number | null
  tag_ids: number[]
  comment_status: 'open' | 'closed'
  sticky: boolean
  menu_order: number
  seo?: SeoFields
}

export interface Autosave {
  revision_id: number
  title: string
  excerpt: string
  content: PMNode
  seo: SeoFields | null
  saved_at: string
}

export interface PostDetail extends Omit<PostInput, 'version'> {
  id: number
  version: number
  url: string | null
  created_at: string
  updated_at: string
  authors: AuthorRef[]
  seo: SeoFields
  featured_media: MediaItem | null
  autosave: Autosave | null
  can_publish: boolean
  can_overwrite: boolean
}

export interface RevisionItem {
  id: number
  kind: 'autosave' | 'manual' | 'publish'
  user_name: string | null
  created_at: string
  title: string
}

export interface Revision extends RevisionItem {
  post_id: number
  excerpt: string
  content: PMNode
  seo: SeoFields | null
}

export interface DiffOp { op: 'equal' | 'insert' | 'delete', text: string }
export interface RevisionDiff {
  title: DiffOp[]
  text: DiffOp[]
  blocks: { index: number, change: 'added' | 'removed' | 'changed', type: string }[]
}

export interface ContentError { path: string, message: string }

// GET /templates
export interface Templates { post: string[], page: string[], allowed_classes: string[] }

export interface MediaItem {
  id: number
  filename: string
  mime: string
  bytes: number
  width: number | null
  height: number | null
  title: string
  alt: string
  caption: string
  description: string
  focal_x: number | null
  focal_y: number | null
  url: string // /m/... original
  thumb_url: string | null // a small variant for grids, images only
  library_visible: boolean
  legacy_path_count: number
  uploaded_by: string | null
  created_at: string
}

export interface MediaDetail extends MediaItem {
  used_in: { id: number, type: PostType, title: string, status: PostStatus }[]
  legacy_paths: { path: string, variant: 'orig' | 'resize', hits: number, last_hit_at: string | null }[]
}

export interface Term {
  id: number
  taxonomy: 'category' | 'tag'
  name: string
  slug: string
  description: string
  parent_id: number | null
  count: number // posts, any status but trash
  url: string | null
}
export interface TermInput { taxonomy: 'category' | 'tag', name: string, slug: string, description: string, parent_id: number | null }

export interface Author {
  id: number
  slug: string
  display_name: string
  bio: string
  website: string
  social: Record<string, string>
  avatar_media_id: number | null
  avatar_url: string | null
  user_id: string | null
  user_name: string | null
  post_count: number
  url: string | null
}
export type AuthorInput = Omit<Author, 'id' | 'avatar_url' | 'user_name' | 'post_count' | 'url'>

export interface MenuItemInput {
  label: string
  kind: 'post' | 'term' | 'author' | 'url'
  target_id: number | null
  url: string | null
  new_tab: boolean
  css_classes: string[]
  children: MenuItemInput[]
}
export interface MenuItem extends MenuItemInput {
  id: number
  resolved_url: string | null // null when the target is gone or unpublished
  target_title: string | null
  children: MenuItem[]
}
export interface Menu { id: number, name: string, slug: string, items: MenuItem[] }

export interface ThemeSettingDecl {
  key: string
  label: string
  type: string
  default: unknown
  options?: string[]
}

export interface SettingsResponse {
  values: Record<string, unknown>
  theme: { name: string, settings: ThemeSettingDecl[] }
  csp_report_only: boolean
}

export interface Dashboard {
  my_drafts: PostListItem[]
  pending_review: PostListItem[] // editors see all, others their own
  scheduled: PostListItem[]
  top_404s: { path: string, hits: number, last_seen: string }[]
  counts: Record<PostStatus, number>
  csp_report_only: boolean
}

export interface SeoResolveInput {
  entity_type: 'post' | 'term' | 'author' | 'home'
  entity_id: number // 0 for a new post or home
  post?: Pick<PostInput, 'type' | 'title' | 'slug' | 'excerpt' | 'content' | 'featured_media_id' | 'category_ids' | 'primary_category_id' | 'published_at' | 'parent_id'>
  seo: SeoFields
}

export type CheckStatus = 'ok' | 'warn' | 'problem'
export interface SeoCheck { id: string, status: CheckStatus, message: string }

export interface SeoResolved {
  title: string
  description: string
  canonical: string
  robots: string
  og: { title: string, description: string, image: string | null, type: string }
  twitter: { card: string, title: string, description: string, image: string | null }
  url: string
  breadcrumbs: { name: string, url: string }[]
  checks: SeoCheck[]
  suggestions: { id: number, title: string, url: string }[]
  unknown_vars: string[]
}

export interface SeoBulkRow {
  post_id: number
  type: PostType
  post_title: string
  status: PostStatus
  url: string | null
  title: string | null
  description: string | null
  resolved_title: string
  resolved_description: string
}

export interface VideoRow {
  id: number | null
  url: string
  provider: string
  title: string
  description: string
  thumbnail_url: string
  duration_seconds: number | null
  upload_date: string | null
  family_friendly: boolean | null
  source: 'auto' | 'manual' | 'import'
}

// redirect manager and 404 log (M2c, docs/ops.md)
export type RedirectMatch = 'exact' | 'prefix' | 'regex'
export interface Redirect {
  id: number
  source: string
  match: RedirectMatch
  target: string | null
  status: number
  hits: number
  last_hit_at: string | null
  note: string
  origin: 'manual' | 'slug_change' | 'import'
  legacy_key: string | null
  created_by: string | null
  created_at: string
  updated_at: string
}
export interface RedirectInput { source: string, match: RedirectMatch, target: string | null, status: number, note: string }
export interface RedirectSaved { redirect: Redirect, warnings: string[], flattened_ids: number[] }
export interface RedirectHop { from: string, to?: string, status: number, via: string, redirect_id?: number }
export interface RedirectConflict {
  message: string
  field: 'target'
  code: 'chain' | 'loop' | 'gone' | 'too_long'
  hops: RedirectHop[]
  flattened_target: string | null
}
export interface RedirectTestResult { path: string, canonical: string, matched: boolean, target?: string }
export interface CsvLineError { line: number, field?: string, message: string }
export interface RedirectImportReport { created: number, updated: number, errors: CsvLineError[], warnings: CsvLineError[] }
export interface NotFoundRow { path: string, hits: number, first_seen: string, last_seen: string, last_referrer: string }
