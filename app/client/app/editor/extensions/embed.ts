import { Node } from '@tiptap/core'
import { tiptapAttrs } from '../schema'

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    embed: { insertEmbed: (attrs: Record<string, unknown>) => ReturnType }
  }
}

export const Embed = Node.create({
  name: 'embed',
  group: 'block',
  atom: true,
  draggable: true,

  addAttributes() {
    return tiptapAttrs('embed')
  },

  parseHTML() {
    return [{ tag: 'div[data-type="embed"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', { 'data-type': 'embed', ...HTMLAttributes }]
  },

  addCommands() {
    return {
      insertEmbed: attrs => ({ commands }) => commands.insertContent({ type: this.name, attrs }),
    }
  },
})
