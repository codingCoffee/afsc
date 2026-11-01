<script setup lang="ts">
import type { Editor } from '@tiptap/vue-3'
import { computed } from 'vue'

const props = defineProps<{ editor: Editor }>()

const headings = computed(() => {
  const out: { level: number, text: string, pos: number }[] = []
  props.editor.state.doc.descendants((node, pos) => {
    if (node.type.name === 'heading' && node.textContent)
      out.push({ level: node.attrs.level, text: node.textContent, pos })
    return node.type.name !== 'heading'
  })
  return out
})

function go(pos: number) {
  props.editor.chain().focus().setTextSelection(pos + 1).scrollIntoView().run()
}
</script>

<template>
  <nav v-if="headings.length" aria-label="Outline" class="grid gap-0.5 text-sm">
    <button
      v-for="h in headings"
      :key="h.pos"
      type="button"
      class="truncate rounded px-2 py-1 text-left text-muted-foreground hover:bg-accent hover:text-foreground"
      :style="{ paddingLeft: `${(h.level - 1) * 0.75 + 0.5}rem` }"
      @click="go(h.pos)"
    >
      {{ h.text }}
    </button>
  </nav>
  <p v-else class="text-xs text-muted-foreground">
    Headings appear here.
  </p>
</template>
