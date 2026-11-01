import { Node } from '@tiptap/core'
import { contentExpr, tiptapAttrs } from '../schema'
import type { MediaNodeOptions } from './image'

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    container: {
      setContainer: (classes: string) => ReturnType
      unsetContainer: () => ReturnType
    }
  }
}

export const Container = Node.create<MediaNodeOptions>({
  name: 'container',
  group: 'block',
  content: contentExpr('container'),
  defining: true,

  addOptions() {
    return { allowedClasses: () => [] }
  },

  addAttributes() {
    return tiptapAttrs('container', { allowedClasses: this.options.allowedClasses })
  },

  parseHTML() {
    return [{ tag: 'div[data-type="container"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', { 'data-type': 'container', ...HTMLAttributes }, 0]
  },

  addCommands() {
    return {
      setContainer: classes => ({ editor, commands }) => editor.isActive(this.name)
        ? commands.updateAttributes(this.name, { classes })
        : commands.wrapIn(this.name, { classes }),
      unsetContainer: () => ({ commands }) => commands.lift(this.name),
    }
  },
})
