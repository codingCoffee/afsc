<script setup lang="ts">
import type { MediaItem } from '@/types/api'
import { ImagePlus, X } from '@lucide/vue'
import { ref, watch } from 'vue'
import MediaPicker from '@/components/media/MediaPicker.vue'
import { Button } from '@/components/ui/button'
import { API } from '@/composables/useAuth'

defineProps<{ placeholder?: string }>()
// settings refs can still hold an unresolved legacy key (a string) until they are replaced
const model = defineModel<number | string | null>({ default: null })

const { authFetch } = useAuth()
const open = ref(false)
const item = ref<MediaItem | null>(null)

watch(model, async (id) => {
  if (typeof id !== 'number') {
    item.value = null
    return
  }
  if (item.value?.id === id)
    return
  try {
    item.value = await authFetch<MediaItem>(`${API}/media/${id}`)
  }
  catch {
    item.value = null
  }
}, { immediate: true })

function pick(items: MediaItem[]) {
  const m = items[0]
  if (!m)
    return
  item.value = m
  model.value = m.id
}
</script>

<template>
  <div class="flex items-center gap-3">
    <div class="grid size-14 shrink-0 place-items-center overflow-hidden rounded-md border bg-muted">
      <img v-if="item" :src="item.thumb_url ?? item.url" :alt="item.alt" class="size-full object-cover">
      <ImagePlus v-else class="size-4 text-muted-foreground" />
    </div>
    <div class="grid min-w-0 flex-1 gap-1">
      <span class="truncate text-sm">
        <template v-if="item">{{ item.filename }}</template>
        <template v-else-if="typeof model === 'string'">Unresolved reference {{ model }}</template>
        <span v-else class="text-muted-foreground">{{ placeholder ?? 'No image' }}</span>
      </span>
      <div class="flex gap-1.5">
        <Button type="button" variant="outline" size="xs" @click="open = true">
          {{ model ? 'Replace' : 'Choose' }}
        </Button>
        <Button v-if="model" type="button" variant="ghost" size="xs" @click="model = null">
          <X />
          Remove
        </Button>
      </div>
    </div>
    <MediaPicker v-model:open="open" accept="image" @select="pick" />
  </div>
</template>
