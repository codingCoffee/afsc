import { Node } from '@tiptap/core'

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    readMore: { insertReadMore: () => ReturnType }
  }
}

export const ReadMore = Node.create({
  name: 'readMore',
  group: 'block',
  atom: true,
  draggable: true,

  parseHTML() {
    return [{ tag: 'div[data-type="read-more"]' }]
  },

  renderHTML() {
    return ['div', { 'data-type': 'read-more' }]
  },

  addCommands() {
    return {
      insertReadMore: () => ({ commands }) => commands.insertContent({ type: this.name }),
    }
  },
})
