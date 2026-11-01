import { API } from '@/composables/useAuth'

export type FieldKind = 'text' | 'number' | 'switch' | 'select' | 'timezone' | 'page' | 'code' | 'csp' | 'color'

export interface FieldDesc {
  key: string
  label: string
  kind: FieldKind
  help?: string
  options?: string[]
  min?: number
  max?: number
  placeholder?: string
}

export interface GroupDesc {
  slug: string
  group: string // API group name
  title: string
  description: string
  fields: FieldDesc[]
}

const num = (key: string, label: string, min: number, max: number, help?: string): FieldDesc =>
  ({ key, label, kind: 'number', min, max, help })

// theme fields come from theme.json and are filled in by the page
export const settingsGroups: GroupDesc[] = [
  {
    slug: 'site',
    group: 'site',
    title: 'Site',
    description: 'Name, language and how dates are shown.',
    fields: [
      { key: 'title', label: 'Site title', kind: 'text' },
      { key: 'tagline', label: 'Tagline', kind: 'text' },
      { key: 'timezone', label: 'Timezone', kind: 'timezone' },
      { key: 'date_format', label: 'Date format', kind: 'text', help: 'A Go layout, for example "January 2, 2006" or "02 Jan 2006".' },
      { key: 'language', label: 'Language', kind: 'text', placeholder: 'en', help: 'Used for <html lang> and og:locale.' },
    ],
  },
  {
    slug: 'permalinks',
    group: 'permalinks',
    title: 'Permalinks',
    description: 'URL structure. Changing it moves URLs; old ones redirect automatically.',
    fields: [
      { key: 'post_pattern', label: 'Post URL pattern', kind: 'text', help: 'Starts with / and contains {slug} or {id}. Tokens: {slug} {year} {month} {day} {id} {category}.' },
      { key: 'category_base', label: 'Category base', kind: 'text' },
      { key: 'tag_base', label: 'Tag base', kind: 'text' },
      { key: 'author_base', label: 'Author base', kind: 'text' },
      { key: 'trailing_slash', label: 'Trailing slash', kind: 'select', options: ['always', 'never'] },
      { key: 'date_archives_enabled', label: 'Date archives', kind: 'switch', help: 'When off, /yyyy/ and /yyyy/mm/ redirect to the home page.' },
      { key: 'author_archives_enabled', label: 'Author archives', kind: 'switch' },
      { key: 'wpcompat_enabled', label: 'WordPress-style URLs', kind: 'switch', help: 'Answer /?p=, /?cat= and similar legacy links.' },
    ],
  },
  {
    slug: 'reading',
    group: 'reading',
    title: 'Reading',
    description: 'What the home page shows and how long lists are.',
    fields: [
      { key: 'home', label: 'Home page', kind: 'page', help: 'Leave empty to show the latest posts.' },
      { key: 'posts_page', label: 'Posts page', kind: 'page', help: 'The page that lists posts when the home page is static.' },
      num('posts_per_page', 'Posts per page', 1, 100),
      num('feed_items', 'Items in feeds', 1, 100),
      { key: 'feed_excerpt_only', label: 'Excerpts only in feeds', kind: 'switch' },
    ],
  },
  {
    slug: 'discussion',
    group: 'discussion',
    title: 'Discussion',
    description: 'Comments, spam filtering and how long comment data is kept.',
    fields: [
      { key: 'comments_enabled', label: 'Comments', kind: 'switch', help: 'When off, no post shows comments or the form.' },
      num('close_after_days', 'Close comments after (days)', 0, 36500, '0 never closes them.'),
      num('thread_depth', 'Thread depth', 1, 10),
      num('comments_per_page', 'Top-level comments per page', 0, 500, '0 shows them all on one page.'),
      num('moderation_threshold', 'Hold for moderation at score', 0, 99, 'Comments scoring this or more wait for approval.'),
      num('spam_threshold', 'Mark as spam at score', 1, 100, 'Must be above the moderation score.'),
      { key: 'trusted_auto_approve', label: 'Auto-approve trusted commenters', kind: 'switch', help: 'Approve low-scoring comments from an email that already has an approved comment.' },
      num('comment_ip_retention_days', 'Keep commenter IPs (days)', 1, 3650),
      num('spam_retention_days', 'Keep spam (days)', 1, 3650),
    ],
  },
  {
    slug: 'performance',
    group: 'cache',
    title: 'Performance',
    description: 'How long pages are cached in memory and by CDNs.',
    fields: [
      num('page_ttl_seconds', 'Page cache TTL (seconds)', 0, 86400, '0 disables the in-memory cache and makes browsers revalidate.'),
    ],
  },
  {
    slug: 'media',
    group: 'media',
    title: 'Media',
    description: 'Upload size limits per file type.',
    fields: [
      num('max_image_mb', 'Images (MB)', 1, 4096),
      num('max_video_mb', 'Video (MB)', 1, 4096),
      num('max_audio_mb', 'Audio (MB)', 1, 4096),
      num('max_pdf_mb', 'PDF (MB)', 1, 4096),
    ],
  },
  {
    slug: 'snippets',
    group: 'snippets',
    title: 'Code snippets',
    description: 'HTML added to every public page. Inline scripts here are allowed by the CSP automatically.',
    fields: [
      { key: 'head_html', label: 'Before </head>', kind: 'code' },
      { key: 'body_end_html', label: 'Before </body>', kind: 'code' },
    ],
  },
  {
    slug: 'security',
    group: 'security',
    title: 'Security',
    description: 'Content Security Policy and HTTPS headers for the public site.',
    fields: [
      { key: 'extra_csp_hosts', label: 'Extra allowed origins', kind: 'csp' },
      { key: 'csp_report_only', label: 'Report-only mode', kind: 'switch', help: 'Browsers log violations instead of blocking. Use it while testing new hosts, then turn it off.' },
      { key: 'hsts', label: 'HSTS', kind: 'switch', help: 'Sends Strict-Transport-Security for a year when the site runs on https.' },
    ],
  },
  {
    slug: 'theme',
    group: 'theme_settings',
    title: 'Theme',
    description: 'Options the active theme declares in theme.json.',
    fields: [],
  },
]

export const cspDirectives = ['script-src', 'style-src', 'img-src', 'font-src', 'connect-src', 'frame-src']

// mirrors settings.ValidCSPHost on the server
const cspHostRe = /^https:\/\/(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+(:\d{1,5})?$/

export function validCspHost(h: string) {
  return cspHostRe.test(h)
}

// csp_report_only for the admin-wide banner; null until first loaded
export function useCspReportOnly() {
  const reportOnly = useState<boolean | null>('settings:csp-report-only', () => null)
  async function refresh() {
    try {
      reportOnly.value = (await useAuth().authFetch<{ csp_report_only: boolean }>(`${API}/dashboard`)).csp_report_only
    }
    catch {
      reportOnly.value = false
    }
  }
  return { reportOnly, refresh }
}
