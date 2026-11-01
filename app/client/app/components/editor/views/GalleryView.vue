<script setup lang="ts">
import { NodeViewContent, nodeViewProps, NodeViewWrapper } from '@tiptap/vue-3'
import { ImagePlus, Trash2 } from '@lucide/vue'
import { computed, inject } from 'vue'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { editorHostKey } from '~/editor/host'

const props = defineProps(nodeViewProps)
const host = inject(editorHostKey)!
const attrs = computed(() => props.node.attrs as Record<string, any>)
const sizes = ['thumbnail', 'medium', 'medium_large', 'large', 'full']

async function addImages() {
  const items = await host.pickMedia('image', true)
  const pos = props.getPos()
  if (!items.length || typeof pos !== 'number')
    return
  const end = pos + props.node.nodeSize - 1
  props.editor.chain().insertContentAt(end, items.map(m => ({ type: 'image', attrs: { mediaId: m.id, alt: m.alt, width: m.width, height: m.height } }))).run()
}
</script>

<template>
  <NodeViewWrapper class="my-4 rounded-md border p-2" :class="{ 'border-primary ring-2 ring-primary/30': selected }">
    <div contenteditable="false" class="mb-2 flex flex-wrap items-center gap-2">
      <span class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">Gallery</span>
      <NativeSelect :model-value="String(attrs.columns)" aria-label="Columns" class="h-8" @update:model-value="updateAttributes({ columns: Number($event) })">
        <NativeSelectOption v-for="n in 9" :key="n" :value="String(n)">
          {{ n }} col
        </NativeSelectOption>
      </NativeSelect>
      <NativeSelect :model-value="attrs.size ?? ''" aria-label="Size" class="h-8" @update:model-value="updateAttributes({ size: $event || null })">
        <NativeSelectOption value="">
          Default size
        </NativeSelectOption>
        <NativeSelectOption v-for="s in sizes" :key="s" :value="s">
          {{ s }}
        </NativeSelectOption>
      </NativeSelect>
      <NativeSelect :model-value="attrs.link ?? 'none'" aria-label="Link" class="h-8" @update:model-value="updateAttributes({ link: $event === 'none' ? null : $event })">
        <NativeSelectOption value="none">
          No link
        </NativeSelectOption>
        <NativeSelectOption value="media">
          Link to file
        </NativeSelectOption>
      </NativeSelect>
      <Button variant="outline" size="sm" @click="addImages">
        <ImagePlus />
        Add
      </Button>
      <Button variant="ghost" size="icon-sm" class="ml-auto text-destructive" aria-label="Remove gallery" @click="deleteNode">
        <Trash2 />
      </Button>
    </div>
    <NodeViewContent class="afsc-gallery grid gap-2" :style="{ gridTemplateColumns: `repeat(${Math.min(attrs.columns, 4)}, minmax(0, 1fr))` }" />
  </NodeViewWrapper>
</template>
