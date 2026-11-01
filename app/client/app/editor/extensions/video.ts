import { Node } from '@tiptap/core'
import { tiptapAttrs } from '../schema'

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    video: { insertVideo: (attrs: Record<string, unknown>) => ReturnType }
  }
}

export const Video = Node.create({
  name: 'video',
  group: 'block',
  atom: true,
  draggable: true,

  addAttributes() {
    return tiptapAttrs('video')
  },

  parseHTML() {
    return [{ tag: 'div[data-type="video"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', { 'data-type': 'video', ...HTMLAttributes }]
  },

  addCommands() {
    return {
      insertVideo: attrs => ({ commands }) => commands.insertContent({ type: this.name, attrs: { controls: true, ...attrs } }),
    }
  },
})
