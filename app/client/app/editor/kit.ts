import type { AnyExtension, NodeViewRenderer } from '@tiptap/core'
import { Extension } from '@tiptap/core'
import { Details, DetailsContent, DetailsSummary } from '@tiptap/extension-details'
import { Link } from '@tiptap/extension-link'
import { ListItem, OrderedList } from '@tiptap/extension-list'
import { Subscript } from '@tiptap/extension-subscript'
import { Superscript } from '@tiptap/extension-superscript'
import { Table, TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import { TextAlign } from '@tiptap/extension-text-align'
import { BackgroundColor, Color, TextStyle } from '@tiptap/extension-text-style'
import { CharacterCount, Placeholder } from '@tiptap/extensions'
import { StarterKit } from '@tiptap/starter-kit'
import { Container } from './extensions/container'
import { Embed } from './extensions/embed'
import { Figcaption } from './extensions/figcaption'
import { Figure } from './extensions/figure'
import { Gallery } from './extensions/gallery'
import { Image } from './extensions/image'
import { RawHtml } from './extensions/rawHtml'
import { ReadMore } from './extensions/readMore'
import { Video } from './extensions/video'
import { EmbedPaste } from './paste'
import { coerce, contentExpr, isColor, safeUrl, schema, tiptapAttrs } from './schema'

export interface KitOptions {
  allowedClasses?: () => string[]
  canRawHtml?: () => boolean
  placeholder?: string
  nodeViews?: Partial<Record<string, NodeViewRenderer>>
}

// the last declaration wins, as in the browser; raw text keeps the author's color format
function styleProp(el: HTMLElement, prop: string): string | null {
  const decl = (el.getAttribute('style') ?? '').split(';').reverse()
    .map(d => d.split(':'))
    .find(([k]) => k?.trim().toLowerCase() === prop)
  return decl ? decl.slice(1).join(':').trim() : null
}

function colorAttr(name: 'color' | 'backgroundColor', css: string) {
  return {
    default: null,
    parseHTML: (el: HTMLElement) => {
      const v = styleProp(el, css)
      return v && isColor(v) ? v : null
    },
    renderHTML: (attrs: Record<string, unknown>) => {
      const v = attrs[name]
      return typeof v === 'string' && isColor(v) ? { style: `${css}: ${v}` } : {}
    },
  }
}

const cellAttrs = (type: 'tableCell' | 'tableHeader') => ({
  ...tiptapAttrs(type, { only: ['colwidth'], html: { colwidth: 'colwidth' } }),
  colspan: { default: 1, parseHTML: (el: HTMLElement) => coerce(el.getAttribute('colspan'), schema.nodes[type]!.attrs!.colspan!) },
  rowspan: { default: 1, parseHTML: (el: HTMLElement) => coerce(el.getAttribute('rowspan'), schema.nodes[type]!.attrs!.rowspan!) },
})

export function buildExtensions(opts: KitOptions = {}): AnyExtension[] {
  const allowedClasses = opts.allowedClasses ?? (() => [])

  const SchemaAttrs = Extension.create({
    name: 'schemaAttrs',
    addGlobalAttributes() {
      return [
        { types: ['paragraph', 'blockquote'], attributes: tiptapAttrs('paragraph', { only: ['classes'], noSplit: ['classes'], allowedClasses }) },
        { types: ['heading'], attributes: tiptapAttrs('heading', { only: ['id', 'classes'], html: { id: 'id' }, noSplit: ['id', 'classes'], allowedClasses }) },
      ]
    },
  })

  const nodes: AnyExtension[] = [
    Image.configure({ allowedClasses }),
    Figure.configure({ allowedClasses }),
    Figcaption,
    Gallery.configure({ allowedClasses }),
    Embed,
    Video,
    Container.configure({ allowedClasses }),
    ReadMore,
    RawHtml.configure({ insertable: opts.canRawHtml ?? (() => false) }),
  ].map((ext) => {
    const view = opts.nodeViews?.[ext.name]
    return view ? ext.extend({ addNodeView: () => view }) : ext
  })

  return [
    StarterKit.configure({
      link: false,
      orderedList: false,
      listItem: false,
      trailingNode: false,
    }),
    OrderedList.extend({ addAttributes: () => tiptapAttrs('orderedList', { html: { start: 'start' } }) }),
    ListItem.extend({ content: contentExpr('listItem') }),
    Link.extend({
      addAttributes: () => tiptapAttrs('link', { html: { href: 'href', target: 'target', rel: 'rel', title: 'title' } }),
    }).configure({
      openOnClick: false,
      autolink: true,
      linkOnPaste: true,
      defaultProtocol: 'https',
      HTMLAttributes: { target: null, rel: null, class: null },
      isAllowedUri: url => safeUrl(url),
    }),
    TextStyle,
    Color.extend({ addGlobalAttributes: () => [{ types: ['textStyle'], attributes: { color: colorAttr('color', 'color') } }] }),
    BackgroundColor.extend({ addGlobalAttributes: () => [{ types: ['textStyle'], attributes: { backgroundColor: colorAttr('backgroundColor', 'background-color') } }] }),
    TextAlign.configure({ types: ['heading', 'paragraph'] }),
    Subscript,
    Superscript,
    Table.configure({ resizable: false }),
    TableRow.extend({ content: contentExpr('tableRow') }),
    TableCell.extend({ addAttributes: () => cellAttrs('tableCell') }),
    TableHeader.extend({ addAttributes: () => cellAttrs('tableHeader') }),
    Details.configure({ persist: false }),
    DetailsSummary.extend({ content: contentExpr('detailsSummary') }),
    DetailsContent,
    SchemaAttrs,
    ...nodes,
    EmbedPaste,
    Placeholder.configure({ placeholder: opts.placeholder ?? 'Write something…' }),
    CharacterCount,
  ]
}
