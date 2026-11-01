<script setup lang="ts">
import type { MediaItem } from '~/types/api'
import { nodeViewProps, NodeViewWrapper } from '@tiptap/vue-3'
import { ImageOff, Replace, Trash2 } from '@lucide/vue'
import { computed, inject, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { editorHostKey } from '~/editor/host'

const props = defineProps(nodeViewProps)
const host = inject(editorHostKey)!

const sizes = ['thumbnail', 'medium', 'medium_large', 'large', 'full']
const aligns = ['none', 'left', 'center', 'right']

const attrs = computed(() => props.node.attrs as Record<string, any>)
const inGallery = computed(() => {
  const pos = props.getPos()
  return typeof pos === 'number' && props.editor.state.doc.resolve(pos).parent.type.name === 'gallery'
})
const item = ref<MediaItem | null>(null)
const preview = computed(() => attrs.value.src || item.value?.thumb_url || item.value?.url || '')

watch(() => attrs.value.mediaId, async (id) => {
  item.value = id ? await host.media(id) : null
}, { immediate: true })

function set(a: Record<string, unknown>) {
  props.updateAttributes(a)
}

async function replace() {
  const [m] = await host.pickMedia('image')
  if (m)
    set({ mediaId: m.id, src: null, alt: m.alt || attrs.value.alt, width: m.width, height: m.height })
}
</script>

<template>
  <NodeViewWrapper class="my-4" :class="{ 'inline-block w-full': inGallery }" data-drag-handle>
    <div class="rounded-md border p-2" :class="selected ? 'border-primary ring-2 ring-primary/30' : 'border-transparent'">
      <div class="grid place-items-center overflow-hidden rounded bg-muted" :class="inGallery ? 'aspect-square' : 'min-h-24'">
        <img v-if="preview" :src="preview" :alt="attrs.alt" class="max-h-96 object-contain" :class="inGallery && 'size-full object-cover'" draggable="false">
        <ImageOff v-else class="size-6 text-muted-foreground" />
      </div>
      <div v-if="selected" contenteditable="false" class="mt-2 grid gap-2 text-xs">
        <Input :model-value="attrs.alt" placeholder="Alt text (describe the image)" class="h-8" @update:model-value="set({ alt: String($event) })" />
        <div v-if="!inGallery" class="flex flex-wrap items-center gap-2">
          <NativeSelect :model-value="attrs.size ?? ''" aria-label="Size" class="h-8" @update:model-value="set({ size: $event || null })">
            <NativeSelectOption value="">
              Default size
            </NativeSelectOption>
            <NativeSelectOption v-for="s in sizes" :key="s" :value="s">
              {{ s }}
            </NativeSelectOption>
            <NativeSelectOption v-if="attrs.size && !sizes.includes(attrs.size)" :value="attrs.size">
              {{ attrs.size }}
            </NativeSelectOption>
          </NativeSelect>
          <ToggleGroup type="single" size="sm" variant="outline" :model-value="attrs.align ?? 'none'" aria-label="Alignment" @update:model-value="set({ align: $event === 'none' ? null : $event })">
            <ToggleGroupItem v-for="a in aligns" :key="a" :value="a" class="px-2 text-[11px]">
              {{ a }}
            </ToggleGroupItem>
          </ToggleGroup>
          <NativeSelect :model-value="attrs.linkTarget ?? 'none'" aria-label="Link" class="h-8" @update:model-value="set({ linkTarget: $event === 'none' ? null : $event, href: $event === 'custom' ? attrs.href : null })">
            <NativeSelectOption value="none">
              No link
            </NativeSelectOption>
            <NativeSelectOption value="media">
              Link to file
            </NativeSelectOption>
            <NativeSelectOption value="custom">
              Custom link
            </NativeSelectOption>
          </NativeSelect>
        </div>
        <Input v-if="attrs.linkTarget === 'custom'" :model-value="attrs.href ?? ''" placeholder="https://" class="h-8" @update:model-value="set({ href: String($event) || null })" />
        <div class="flex gap-2">
          <Button variant="outline" size="sm" @click="replace">
            <Replace />
            Replace
          </Button>
          <Button variant="ghost" size="sm" class="text-destructive" @click="deleteNode">
            <Trash2 />
            Remove
          </Button>
        </div>
      </div>
    </div>
  </NodeViewWrapper>
</template>
