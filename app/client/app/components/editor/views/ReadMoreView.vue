<script setup lang="ts">
import { nodeViewProps, NodeViewWrapper } from '@tiptap/vue-3'

defineProps(nodeViewProps)
</script>

<template>
  <NodeViewWrapper class="my-6 flex items-center gap-3" data-drag-handle>
    <div class="h-px flex-1 border-t border-dashed" :class="selected ? 'border-primary' : 'border-muted-foreground/40'" />
    <span class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">Read more</span>
    <div class="h-px flex-1 border-t border-dashed" :class="selected ? 'border-primary' : 'border-muted-foreground/40'" />
  </NodeViewWrapper>
</template>
