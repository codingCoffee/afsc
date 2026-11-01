import type { SeoFields } from '@/types/api'
import { toast } from 'vue-sonner'

// Yoast-compatible variables (build plan §8.2); unknown ones render empty and are reported by /seo/resolve
export const seoVars: { name: string, help: string }[] = [
  { name: 'title', help: 'Post, page, term or author title' },
  { name: 'sitename', help: 'Site title' },
  { name: 'sitedesc', help: 'Site tagline' },
  { name: 'sep', help: 'Separator' },
  { name: 'page', help: '"Page N of M" on page 2 and later' },
  { name: 'pagenumber', help: 'Current page number' },
  { name: 'pagetotal', help: 'Number of pages' },
  { name: 'primary_category', help: 'Primary category' },
  { name: 'category', help: 'All categories' },
  { name: 'tag', help: 'All tags' },
  { name: 'term_title', help: 'Archive term name' },
  { name: 'term_description', help: 'Archive term description' },
  { name: 'name', help: 'Author name' },
  { name: 'author_first_name', help: 'Author first name' },
  { name: 'author_last_name', help: 'Author last name' },
  { name: 'excerpt', help: 'Excerpt, else the start of the content' },
  { name: 'excerpt_only', help: 'Excerpt only' },
  { name: 'date', help: 'Publish date, or the archive date' },
  { name: 'modified', help: 'Last modified date' },
  { name: 'currentyear', help: 'This year' },
  { name: 'currentmonth', help: 'This month' },
  { name: 'searchphrase', help: 'Search query' },
  { name: 'pt_single', help: '"Post" or "Page"' },
  { name: 'pt_plural', help: '"Posts" or "Pages"' },
]

// must match internal/seo/checks.go; Google cuts by width: about 580 px of 20px Arial and 920 px of 14px Arial
export const seoLimits = {
  title: { min: 30, max: 60, px: 580, font: '20px Arial' },
  description: { min: 120, max: 156, px: 920, font: '14px Arial' },
} as const

let ctx: CanvasRenderingContext2D | null | undefined
export function pixelWidth(text: string, font: string): number {
  if (ctx === undefined)
    ctx = document.createElement('canvas').getContext('2d')
  if (!ctx)
    return 0
  ctx.font = font
  return ctx.measureText(text).width
}

export function emptySeo(): SeoFields {
  return {
    title: null,
    description: null,
    canonical: null,
    robots_noindex: null,
    robots_nofollow: null,
    robots_advanced: null,
    og_title: null,
    og_description: null,
    og_image_media_id: null,
    twitter_title: null,
    twitter_description: null,
    twitter_image_media_id: null,
    focus_keyphrase: null,
    breadcrumb_title: null,
    is_cornerstone: false,
  }
}

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success('Copied.')
  }
  catch {
    toast.error('Could not copy. Select the text and copy it yourself.')
  }
}
