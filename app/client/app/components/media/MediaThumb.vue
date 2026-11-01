<script setup lang="ts">
import type { MediaItem } from '@/types/api'
import { FileText, Film, Music } from '@lucide/vue'
import { computed } from 'vue'

const props = defineProps<{ item: MediaItem }>()
const icon = computed(() => {
  if (props.item.mime.startsWith('video/'))
    return Film
  if (props.item.mime.startsWith('audio/'))
    return Music
  return FileText
})
</script>

<template>
  <div class="flex items-center justify-center overflow-hidden bg-muted/40">
    <img
      v-if="item.thumb_url"
      :src="item.thumb_url"
      :alt="item.alt"
      loading="lazy"
      class="size-full object-cover"
      :style="item.focal_x != null && item.focal_y != null ? { objectPosition: `${item.focal_x * 100}% ${item.focal_y * 100}%` } : undefined"
    >
    <div v-else class="flex flex-col items-center gap-1 p-2 text-muted-foreground">
      <component :is="icon" class="size-6" />
      <span class="font-mono text-[10px] uppercase">{{ item.mime.split('/')[1] }}</span>
    </div>
  </div>
</template>
