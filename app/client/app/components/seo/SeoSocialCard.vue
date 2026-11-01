<script setup lang="ts">
import { ImageOff } from '@lucide/vue'
import { computed } from 'vue'

const props = defineProps<{ title: string, description: string, image: string | null, url: string }>()
const host = computed(() => {
  try {
    return new URL(props.url).host
  }
  catch {
    return ''
  }
})
</script>

<template>
  <div class="grid gap-2">
    <span class="font-mono text-[11px] tracking-widest text-muted-foreground uppercase">Social preview</span>
    <div class="max-w-[500px] overflow-hidden rounded-md border bg-card">
      <div class="aspect-[1.91/1] bg-muted">
        <img v-if="image" :src="image" alt="" class="size-full object-cover">
        <div v-else class="grid size-full place-items-center text-xs text-muted-foreground">
          <span class="flex items-center gap-1.5"><ImageOff class="size-4" /> No image</span>
        </div>
      </div>
      <div class="grid gap-0.5 border-t px-3 py-2">
        <span class="truncate text-[11px] text-muted-foreground uppercase">{{ host }}</span>
        <span class="line-clamp-1 text-sm font-semibold">{{ title }}</span>
        <span class="line-clamp-2 text-xs text-muted-foreground">{{ description }}</span>
      </div>
    </div>
  </div>
</template>
