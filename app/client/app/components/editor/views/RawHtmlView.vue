<script setup lang="ts">
import { nodeViewProps, NodeViewWrapper } from '@tiptap/vue-3'
import { Code, Trash2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

defineProps(nodeViewProps)
</script>

<template>
  <NodeViewWrapper class="my-4" data-drag-handle>
    <div class="rounded-md border p-2" :class="{ 'border-primary ring-2 ring-primary/30': selected }">
      <div contenteditable="false" class="mb-2 flex items-center gap-2">
        <Code class="size-4 text-muted-foreground" />
        <span class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">Raw HTML · sanitized on save</span>
        <Button variant="ghost" size="icon-sm" class="ml-auto text-destructive" aria-label="Remove raw HTML" :disabled="!editor.isEditable" @click="deleteNode">
          <Trash2 />
        </Button>
      </div>
      <Textarea
        contenteditable="false"
        :model-value="node.attrs.html"
        rows="6"
        spellcheck="false"
        class="font-mono text-xs"
        :readonly="!editor.isEditable"
        @update:model-value="updateAttributes({ html: String($event) })"
      />
    </div>
  </NodeViewWrapper>
</template>
