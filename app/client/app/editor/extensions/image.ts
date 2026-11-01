import { mergeAttributes, Node } from '@tiptap/core'
import { safeUrl, tiptapAttrs } from '../schema'

export interface MediaNodeOptions { allowedClasses: () => string[] }

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    image: { insertImage: (attrs: Record<string, unknown>) => ReturnType }
  }
}

export const Image = Node.create<MediaNodeOptions>({
  name: 'image',
  group: 'block',
  atom: true,
  draggable: true,

  addOptions() {
    return { allowedClasses: () => [] }
  },

  addAttributes() {
    return tiptapAttrs('image', {
      html: { src: 'src', alt: 'alt', title: 'title', width: 'width', height: 'height' },
      allowedClasses: this.options.allowedClasses,
    })
  },

  parseHTML() {
    return [{
      tag: 'img',
      getAttrs: (el) => {
        const src = el.getAttribute('src') ?? ''
        return el.hasAttribute('data-media-id') || (src !== '' && safeUrl(src)) ? {} : false
      },
    }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['img', mergeAttributes(HTMLAttributes)]
  },

  addCommands() {
    return {
      insertImage: attrs => ({ commands }) => commands.insertContent({ type: this.name, attrs }),
    }
  },
})
