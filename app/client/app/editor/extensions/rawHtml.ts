import { Node } from '@tiptap/core'
import { tiptapAttrs } from '../schema'

export interface RawHtmlOptions { insertable: () => boolean }

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    rawHtml: { insertRawHtml: () => ReturnType }
  }
}

// Existing rawHtml blocks always load; inserting new ones needs content.raw_html (the server enforces it too).
export const RawHtml = Node.create<RawHtmlOptions>({
  name: 'rawHtml',
  group: 'block',
  atom: true,
  draggable: true,

  addOptions() {
    return { insertable: () => false }
  },

  addAttributes() {
    return tiptapAttrs('rawHtml')
  },

  parseHTML() {
    return [{ tag: 'div[data-type="raw-html"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', { 'data-type': 'raw-html', ...HTMLAttributes }]
  },

  addCommands() {
    return {
      insertRawHtml: () => ({ commands }) => this.options.insertable() && commands.insertContent({ type: this.name, attrs: { html: '' } }),
    }
  },
})
