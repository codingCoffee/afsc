<script setup lang="ts">
import type { MediaItem } from '~/types/api'
import { nodeViewProps, NodeViewWrapper } from '@tiptap/vue-3'
import { Film, Replace, Trash2 } from '@lucide/vue'
import { computed, inject, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { editorHostKey } from '~/editor/host'

const props = defineProps(nodeViewProps)
const host = inject(editorHostKey)!
const attrs = computed(() => props.node.attrs as Record<string, any>)
const flags = ['controls', 'autoplay', 'loop', 'muted'] as const

const item = ref<MediaItem | null>(null)
const poster = ref<MediaItem | null>(null)
watch(() => attrs.value.mediaId, async id => item.value = id ? await host.media(id) : null, { immediate: true })
watch(() => attrs.value.posterMediaId, async id => poster.value = id ? await host.media(id) : null, { immediate: true })

async function pickVideo() {
  const [m] = await host.pickMedia('video')
  if (m)
    props.updateAttributes({ mediaId: m.id, src: null })
}

async function pickPoster() {
  const [m] = await host.pickMedia('image')
  if (m)
    props.updateAttributes({ posterMediaId: m.id })
}
</script>

<template>
  <NodeViewWrapper class="my-4" data-drag-handle>
    <div class="rounded-md border bg-muted/40 p-3" :class="{ 'border-primary ring-2 ring-primary/30': selected }">
      <div class="flex items-center gap-3">
        <img v-if="poster?.thumb_url" :src="poster.thumb_url" alt="" class="h-12 w-20 rounded object-cover">
        <Film v-else class="size-5 shrink-0 text-muted-foreground" />
        <div class="min-w-0 truncate text-sm">
          {{ item?.filename ?? attrs.src ?? 'No video selected' }}
        </div>
      </div>
      <div v-if="selected" contenteditable="false" class="mt-3 grid gap-3">
        <div class="flex flex-wrap gap-4">
          <div v-for="f in flags" :key="f" class="flex items-center gap-2">
            <Checkbox :id="`video-${f}`" :model-value="!!attrs[f]" @update:model-value="updateAttributes({ [f]: !!$event })" />
            <Label :for="`video-${f}`" class="text-xs">{{ f }}</Label>
          </div>
        </div>
        <div class="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" @click="pickVideo">
            <Replace />
            Video
          </Button>
          <Button variant="outline" size="sm" @click="pickPoster">
            Poster
          </Button>
          <Button v-if="attrs.posterMediaId" variant="ghost" size="sm" @click="updateAttributes({ posterMediaId: null })">
            Clear poster
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
