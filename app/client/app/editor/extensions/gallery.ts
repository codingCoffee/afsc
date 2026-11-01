import { Node } from '@tiptap/core'
import { contentExpr, tiptapAttrs } from '../schema'
import type { MediaNodeOptions } from './image'

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    gallery: { insertGallery: (images: Record<string, unknown>[]) => ReturnType }
  }
}

export const Gallery = Node.create<MediaNodeOptions>({
  name: 'gallery',
  group: 'block',
  content: contentExpr('gallery'),
  draggable: true,
  isolating: true,

  addOptions() {
    return { allowedClasses: () => [] }
  },

  addAttributes() {
    return tiptapAttrs('gallery', { allowedClasses: this.options.allowedClasses })
  },

  parseHTML() {
    return [{ tag: 'div[data-type="gallery"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', { 'data-type': 'gallery', ...HTMLAttributes }, 0]
  },

  addCommands() {
    return {
      insertGallery: images => ({ commands }) => images.length > 0 && commands.insertContent({
        type: this.name,
        attrs: { columns: Math.min(images.length, 3) },
        content: images.map(attrs => ({ type: 'image', attrs })),
      }),
    }
  },
})
