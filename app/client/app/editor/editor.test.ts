import type { JSONNode } from './schema'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { GlobalRegistrator } from '@happy-dom/global-registrator'
import { afterAll, describe, expect, test } from 'bun:test'

GlobalRegistrator.register()

const { Editor } = await import('@tiptap/core')
const { buildExtensions } = await import('./kit')
const { embedFromUrl } = await import('./paste')
const { contentExpr, defaultFor, normalizeDoc, schema } = await import('./schema')

const allowedClasses = ['lead', 'callout']
const extensions = () => buildExtensions({ allowedClasses: () => allowedClasses, canRawHtml: () => true })

const doc = (...content: JSONNode[]): JSONNode => ({ type: 'doc', content })
const p = (text: string, attrs?: Record<string, unknown>): JSONNode => ({ type: 'paragraph', ...(attrs ? { attrs } : {}), content: [{ type: 'text', text }] })
const cell = (type: string, attrs: Record<string, unknown>, text: string): JSONNode => ({ type, attrs, content: [p(text)] })

// one document per node (and mark) with every attr set; image and video take mediaId or src, so each gets two
const fixtures: Record<string, JSONNode> = {
  'all-nodes': JSON.parse(readFileSync(join(import.meta.dir, '../../../internal/content/testdata/all-nodes.json'), 'utf8')),
  'paragraph': doc(p('Centered', { textAlign: 'center', classes: 'lead' })),
  'heading': doc({ type: 'heading', attrs: { level: 3, textAlign: 'right', id: 'sec-1', classes: 'lead' }, content: [{ type: 'text', text: 'Title' }] }),
  'blockquote': doc({ type: 'blockquote', attrs: { classes: 'callout' }, content: [p('Quote')] }),
  'lists': doc(
    { type: 'orderedList', attrs: { start: 5 }, content: [{ type: 'listItem', content: [p('Five')] }] },
    { type: 'bulletList', content: [{ type: 'listItem', content: [{ type: 'heading', attrs: { level: 4 }, content: [{ type: 'text', text: 'Block first' }] }] }] },
  ),
  'codeBlock': doc({ type: 'codeBlock', attrs: { language: 'ts' }, content: [{ type: 'text', text: 'const a = 1\n  <b>' }] }),
  'horizontalRule': doc({ type: 'horizontalRule' }, { type: 'readMore' }),
  'image-media': doc({ type: 'image', attrs: { mediaId: 3, alt: 'Chart', title: 'T', width: 10, height: 20, align: 'center', size: 'large', href: 'https://example.com/x', linkTarget: 'custom', classes: 'lead' } }),
  'image-src': doc({ type: 'image', attrs: { src: 'https://example.com/a.png', alt: '2024', title: '1', width: 640, height: 480, align: 'none', size: 'full', linkTarget: 'none', classes: 'callout' } }),
  'figure': doc({ type: 'figure', attrs: { align: 'right', classes: 'callout' }, content: [
    { type: 'image', attrs: { mediaId: 3, alt: 'Captioned', linkTarget: 'media' } },
    { type: 'figcaption', content: [{ type: 'text', text: 'Caption ' }, { type: 'text', text: 'bold', marks: [{ type: 'bold' }] }, { type: 'hardBreak' }, { type: 'text', text: 'two' }] },
  ] }),
  'gallery': doc({ type: 'gallery', attrs: { columns: 2, size: 'thumbnail', link: 'none', classes: 'lead' }, content: [
    { type: 'image', attrs: { mediaId: 3, alt: 'One' } },
    { type: 'image', attrs: { src: '/two.jpg', alt: 'Two' } },
  ] }),
  'embed': doc({ type: 'embed', attrs: { provider: 'youtube', url: 'https://www.youtube.com/watch?v=XXXXXXXXXXX', externalId: 'XXXXXXXXXXX', width: 560, height: 315, caption: 'Clip' } }),
  'video-media': doc({ type: 'video', attrs: { mediaId: 4, posterMediaId: 3, controls: true, autoplay: true, loop: false, muted: true } }),
  'video-src': doc({ type: 'video', attrs: { src: 'https://example.com/v.mp4', controls: false, autoplay: false, loop: true, muted: false } }),
  'table': doc({ type: 'table', content: [
    { type: 'tableRow', content: [cell('tableHeader', { colspan: 2, rowspan: 1, colwidth: [100, 50] }, 'A'), cell('tableHeader', { colspan: 1, rowspan: 2, colwidth: [80] }, 'B')] },
    { type: 'tableRow', content: [cell('tableCell', { colspan: 1, rowspan: 1, colwidth: [100] }, 'C'), cell('tableCell', { colspan: 1, rowspan: 1, colwidth: [50] }, 'D')] },
  ] }),
  'details': doc({ type: 'details', content: [
    { type: 'detailsSummary', content: [{ type: 'text', text: 'More ' }, { type: 'text', text: 'here', marks: [{ type: 'italic' }] }] },
    { type: 'detailsContent', content: [p('Hidden')] },
  ] }),
  'container': doc({ type: 'container', attrs: { classes: 'callout lead' }, content: [p('Boxed'), { type: 'blockquote', content: [p('Nested')] }] }),
  'rawHtml': doc({ type: 'rawHtml', attrs: { html: '<p class="x">Hi & "bye"</p>' } }),
  'marks': doc({ type: 'paragraph', content: [
    { type: 'text', text: 'all', marks: [
      { type: 'link', attrs: { href: 'mailto:someone@example.com', target: '_blank', rel: 'noopener nofollow', title: 'Mail' } },
      { type: 'bold' },
      { type: 'italic' },
      { type: 'underline' },
      { type: 'strike' },
      { type: 'textStyle', attrs: { color: '#112233', backgroundColor: 'rgb(1, 2, 3)' } },
    ] },
    { type: 'text', text: ' ' },
    { type: 'text', text: 'x', marks: [{ type: 'code' }] },
    { type: 'text', text: '2', marks: [{ type: 'subscript' }] },
    { type: 'text', text: '3', marks: [{ type: 'superscript' }] },
    { type: 'text', text: 'anchor', marks: [{ type: 'link', attrs: { href: '#intro' } }] },
  ] }),
}

describe('schema parity', () => {
  const editor = new Editor({ extensions: extensions() })
  const s = editor.schema
  afterAll(() => editor.destroy())

  test('editor nodes and marks are all in schema.json', () => {
    expect(Object.keys(s.nodes).filter(n => !schema.nodes[n])).toEqual([])
    expect(Object.keys(s.marks).filter(m => !schema.marks[m])).toEqual([])
  })

  for (const [name, spec] of Object.entries(schema.nodes)) {
    test(`node ${name}`, () => {
      const t = s.nodes[name]
      expect(t).toBeDefined()
      if (!t)
        return
      const attrs = Object.keys(spec.attrs ?? {}).sort()
      expect(Object.keys(t.attrs).sort()).toEqual(attrs)
      for (const a of attrs)
        expect([a, t.attrs[a]?.default]).toEqual([a, defaultFor(name, a)])
      expect((t.spec.content ?? '').replace(/\s*\|\s*/g, ' | ')).toBe(contentExpr(name))
      const groups = (t.spec.group ?? '').split(' ')
      if (spec.group)
        expect(groups).toContain(spec.group)
      else
        expect(groups.filter(g => g === 'block' || g === 'inline')).toEqual([])
      if (spec.marks)
        expect(t.allowsMarkType(s.marks.bold!)).toBe(true)
      else if (t.inlineContent)
        expect(t.allowsMarkType(s.marks.bold!)).toBe(false)
    })
  }

  for (const [name, spec] of Object.entries(schema.marks)) {
    test(`mark ${name}`, () => {
      const t = s.marks[name]
      expect(t).toBeDefined()
      if (!t)
        return
      const attrs = Object.keys(spec.attrs ?? {}).sort()
      expect(Object.keys(t.attrs).sort()).toEqual(attrs)
      for (const a of attrs)
        expect([a, t.attrs[a]?.default]).toEqual([a, defaultFor(name, a)])
    })
  }
})

const output: Record<string, unknown> = {}

describe('round trip', () => {
  for (const [name, fixture] of Object.entries(fixtures)) {
    test(name, () => {
      const want = normalizeDoc(fixture)
      const a = new Editor({ extensions: extensions(), content: fixture })
      const json = a.getJSON() as JSONNode
      const html = a.getHTML()
      a.destroy()
      output[name] = json
      expect(normalizeDoc(json)).toEqual(want)

      const b = new Editor({ extensions: extensions(), content: html })
      const again = b.getJSON() as JSONNode
      b.destroy()
      expect(normalizeDoc(again)).toEqual(want)
    })
  }

  afterAll(() => {
    const dir = join(import.meta.dir, '../../.cache')
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, 'editor-roundtrip.json'), `${JSON.stringify({ allowed_classes: allowedClasses, docs: output }, null, 2)}\n`)
  })
})

describe('paste url', () => {
  const cases: [string, ReturnType<typeof embedFromUrl>][] = [
    ['https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=3', { provider: 'youtube', url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', externalId: 'dQw4w9WgXcQ' }],
    ['https://youtu.be/dQw4w9WgXcQ', { provider: 'youtube', url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', externalId: 'dQw4w9WgXcQ' }],
    ['  http://m.youtube.com/shorts/dQw4w9WgXcQ\n', { provider: 'youtube', url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', externalId: 'dQw4w9WgXcQ' }],
    ['https://www.youtube.com/watch?v=short', null],
    ['https://vimeo.com/123456', { provider: 'vimeo', url: 'https://vimeo.com/123456', externalId: '123456' }],
    ['https://player.vimeo.com/video/987?h=1', { provider: 'vimeo', url: 'https://vimeo.com/987', externalId: '987' }],
    ['https://twitter.com/example/status/12345', { provider: 'x', url: 'https://x.com/example/status/12345' }],
    ['https://x.com/example', null],
    ['https://instagram.com/p/AbC_1/', { provider: 'instagram', url: 'https://www.instagram.com/p/AbC_1/' }],
    ['https://open.spotify.com/intl-de/track/abc123XYZ?si=x', { provider: 'spotify', url: 'https://open.spotify.com/track/abc123XYZ' }],
    ['https://soundcloud.com/artist/track/', { provider: 'soundcloud', url: 'https://soundcloud.com/artist/track' }],
    ['https://soundcloud.com/artist', null],
    ['https://www.google.com/maps/embed?pb=example', { provider: 'googlemaps', url: 'https://www.google.com/maps/embed?pb=example' }],
    ['https://www.google.com/maps/place/x', null],
    ['https://example.com/video', null],
    ['see https://youtu.be/dQw4w9WgXcQ', null],
    ['javascript:alert(1)', null],
    ['', null],
  ]
  for (const [input, want] of cases) {
    test(JSON.stringify(input), () => {
      expect(embedFromUrl(input)).toEqual(want)
    })
  }

  test('every provider has a matcher', () => {
    for (const provider of schema.embed_providers)
      expect(cases.some(([, w]) => w?.provider === provider)).toBe(true)
  })
})
