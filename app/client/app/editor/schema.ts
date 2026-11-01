import type { Attribute } from '@tiptap/core'
import schemaJSON from './schema.json'

export type AttrType = 'string' | 'integer' | 'boolean' | 'enum' | 'url' | 'classes' | 'color' | 'integer[]' | 'mediaId'

export interface AttrSpec {
  type: AttrType
  required?: boolean
  enum?: string[]
  min?: number
  max?: number
  pattern?: string
}

export interface ChildRule { allow: string[], min: number, max: number }
export interface NodeSpec { group?: string, content?: ChildRule[], attrs?: Record<string, AttrSpec>, marks?: boolean }
export interface MarkSpec { attrs?: Record<string, AttrSpec> }

export interface ContentSchema {
  nodes: Record<string, NodeSpec>
  marks: Record<string, MarkSpec>
  mark_order: string[]
  embed_providers: string[]
}

export interface JSONNode {
  type: string
  attrs?: Record<string, unknown>
  content?: JSONNode[]
  marks?: { type: string, attrs?: Record<string, unknown> }[]
  text?: string
}

export const schema = schemaJSON as ContentSchema

// Editor defaults. Optional attrs default to null (absent) unless listed in implicitDefaults.
const requiredDefaults: Record<string, unknown> = {
  'heading.level': 1,
  'image.alt': '',
  'gallery.columns': 3,
  'rawHtml.html': '',
}
const implicitDefaults: Record<string, unknown> = {
  'orderedList.start': 1,
  'tableCell.colspan': 1,
  'tableCell.rowspan': 1,
  'tableHeader.colspan': 1,
  'tableHeader.rowspan': 1,
}

export function attrSpecs(type: string): Record<string, AttrSpec> {
  return schema.nodes[type]?.attrs ?? schema.marks[type]?.attrs ?? {}
}

export function defaultFor(type: string, attr: string): unknown {
  const key = `${type}.${attr}`
  return attrSpecs(type)[attr]?.required ? (requiredDefaults[key] ?? null) : (implicitDefaults[key] ?? null)
}

export function contentExpr(type: string): string {
  return (schema.nodes[type]?.content ?? []).map((r) => {
    const name = r.allow.length > 1 ? `(${r.allow.join(' | ')})` : r.allow[0]
    const q = r.min === 1 && r.max === 1 ? '' : r.min === 0 ? '*' : '+'
    return name + q
  }).join(' ')
}

const classRe = /^[A-Z_-][\w-]{0,63}$/i
const colorRe = /^(?:#[0-9a-f]{3,4}|#[0-9a-f]{6}|#[0-9a-f]{8}|rgb\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*\))$/i

// Mirrors sanitize.SafeURL: http, https, mailto, tel, relative paths and #anchors.
export function safeUrl(v: string): boolean {
  // eslint-disable-next-line no-control-regex
  const s = v.replace(/[\u0000-\u001F\u007F]/g, '')
  const i = s.search(/[:/?#]/)
  if (i < 0 || s[i] !== ':')
    return true
  return ['http', 'https', 'mailto', 'tel'].includes(s.slice(0, i).toLowerCase())
}

export function isColor(v: string): boolean {
  return colorRe.test(v)
}

export function cleanClasses(v: string, allowed: string[]): string | null {
  const kept = v.split(/\s+/).filter(c => c && classRe.test(c) && allowed.includes(c))
  return kept.length ? kept.join(' ') : null
}

// coerce turns an HTML attribute string into a schema-valid value, or null when it isn't one.
export function coerce(raw: string | null, spec: AttrSpec, allowedClasses: string[] = []): unknown {
  if (raw == null)
    return null
  switch (spec.type) {
    case 'integer':
    case 'mediaId': {
      if (!/^-?\d+$/.test(raw.trim()))
        return null
      const n = Number(raw)
      const min = spec.type === 'mediaId' ? 1 : spec.min
      if ((min != null && n < min) || (spec.max != null && n > spec.max))
        return null
      return n
    }
    case 'boolean':
      return raw === 'true' ? true : raw === 'false' ? false : null
    case 'enum':
      return spec.enum?.includes(raw) ? raw : null
    case 'url':
      return raw !== '' && safeUrl(raw) ? raw : null
    case 'color':
      return isColor(raw) ? raw : null
    case 'classes':
      return cleanClasses(raw, allowedClasses)
    case 'integer[]': {
      const list = raw.split(',').map(s => s.trim())
      return list.every(s => /^\d+$/.test(s)) ? list.map(Number) : null
    }
    default:
      if (spec.pattern && !new RegExp(spec.pattern).test(raw))
        return null
      return raw
  }
}

function kebab(s: string) {
  return s.replace(/[A-Z]/g, c => `-${c.toLowerCase()}`)
}

export interface AttrOptions {
  only?: string[]
  // HTML attribute names used instead of data-<kebab name>
  html?: Record<string, string>
  noSplit?: string[]
  allowedClasses?: () => string[]
}

// tiptapAttrs builds Tiptap attributes for a schema node or mark, so names, defaults and parsing follow schema.json.
export function tiptapAttrs(type: string, opts: AttrOptions = {}): Record<string, Attribute> {
  const out: Record<string, Attribute> = {}
  for (const [name, spec] of Object.entries(attrSpecs(type))) {
    if (opts.only && !opts.only.includes(name))
      continue
    const htmlName = opts.html?.[name] ?? `data-${kebab(name)}`
    out[name] = {
      default: defaultFor(type, name),
      keepOnSplit: !opts.noSplit?.includes(name),
      parseHTML: el => coerce(el.getAttribute(htmlName), spec, opts.allowedClasses?.() ?? []),
      renderHTML: (attrs) => {
        const v = attrs[name]
        if (v == null)
          return {}
        const s = Array.isArray(v) ? v.join(',') : String(v)
        return spec.type === 'classes' && !opts.html?.[name] ? { 'class': s, 'data-classes': s } : { [htmlName]: s }
      },
    }
  }
  return out
}

function sameValue(a: unknown, b: unknown) {
  return JSON.stringify(a) === JSON.stringify(b)
}

function cleanAttrs(type: string, attrs?: Record<string, unknown>) {
  if (!attrs)
    return undefined
  const specs = attrSpecs(type)
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(attrs)) {
    const spec = specs[k]
    if (!spec || v == null || (!spec.required && sameValue(v, defaultFor(type, k))))
      continue
    out[k] = v
  }
  return Object.keys(out).length ? out : undefined
}

// normalizeDoc projects editor JSON onto schema.json: unknown, null and default-valued optional attrs are dropped,
// empty textStyle marks removed and marks put in mark_order. It is the form sent to the API.
export function normalizeDoc(node: JSONNode): JSONNode {
  const out: JSONNode = { type: node.type }
  const attrs = cleanAttrs(node.type, node.attrs)
  if (attrs)
    out.attrs = attrs
  if (node.text != null)
    out.text = node.text
  const marks = (node.marks ?? [])
    .filter(m => schema.marks[m.type])
    .map((m) => {
      const a = cleanAttrs(m.type, m.attrs)
      return a ? { type: m.type, attrs: a } : { type: m.type }
    })
    .filter(m => m.type !== 'textStyle' || m.attrs)
    .sort((a, b) => schema.mark_order.indexOf(a.type) - schema.mark_order.indexOf(b.type))
  if (marks.length)
    out.marks = marks
  if (node.content?.length)
    out.content = node.content.map(normalizeDoc)
  return out
}

export function emptyDoc(): JSONNode {
  return { type: 'doc', content: [{ type: 'paragraph' }] }
}
