import { Node } from '@tiptap/core'
import { contentExpr, tiptapAttrs } from '../schema'
import type { MediaNodeOptions } from './image'

export const Figure = Node.create<MediaNodeOptions>({
  name: 'figure',
  group: 'block',
  content: contentExpr('figure'),
  draggable: true,
  isolating: true,

  addOptions() {
    return { allowedClasses: () => [] }
  },

  addAttributes() {
    return tiptapAttrs('figure', { allowedClasses: this.options.allowedClasses })
  },

  parseHTML() {
    return [{ tag: 'figure' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['figure', HTMLAttributes, 0]
  },
})
