import { Node } from '@tiptap/core'
import { contentExpr } from '../schema'

export const Figcaption = Node.create({
  name: 'figcaption',
  content: contentExpr('figcaption'),

  parseHTML() {
    return [{ tag: 'figcaption' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['figcaption', HTMLAttributes, 0]
  },
})
