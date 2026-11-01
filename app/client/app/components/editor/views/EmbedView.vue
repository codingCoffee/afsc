<script setup lang="ts">
import { nodeViewProps, NodeViewWrapper } from '@tiptap/vue-3'
import { Clapperboard, Trash2 } from '@lucide/vue'
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { embedFromUrl } from '~/editor/paste'

const props = defineProps(nodeViewProps)
const attrs = computed(() => props.node.attrs as Record<string, any>)
const draft = ref('')
const error = ref('')

function applyUrl() {
  const hit = embedFromUrl(draft.value)
  if (!hit) {
    error.value = 'Not a supported YouTube, Vimeo, X, Instagram, Spotify, SoundCloud or Google Maps embed URL.'
    return
  }
  error.value = ''
  props.updateAttributes({ ...hit, externalId: hit.externalId ?? null })
  draft.value = ''
}
</script>

<template>
  <NodeViewWrapper class="my-4" data-drag-handle>
    <div class="rounded-md border bg-muted/40 p-3" :class="{ 'border-primary ring-2 ring-primary/30': selected }">
      <div class="flex items-center gap-3">
        <Clapperboard class="size-5 shrink-0 text-muted-foreground" />
        <div class="min-w-0">
          <div class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
            {{ attrs.provider ?? 'embed' }}
          </div>
          <div class="truncate text-sm">
            {{ attrs.url ?? 'No URL yet' }}
          </div>
        </div>
      </div>
      <div v-if="selected || !attrs.url" contenteditable="false" class="mt-3 grid gap-2">
        <div class="flex gap-2">
          <Input v-model="draft" placeholder="Paste a new URL" class="h-8" @keydown.enter.prevent="applyUrl" />
          <Button size="sm" variant="outline" :disabled="!draft" @click="applyUrl">
            Set
          </Button>
        </div>
        <p v-if="error" class="text-xs text-destructive">
          {{ error }}
        </p>
        <Input :model-value="attrs.caption ?? ''" placeholder="Caption (optional)" class="h-8" @update:model-value="updateAttributes({ caption: String($event) || null })" />
        <Button variant="ghost" size="sm" class="justify-self-start text-destructive" @click="deleteNode">
          <Trash2 />
          Remove
        </Button>
      </div>
    </div>
  </NodeViewWrapper>
</template>
