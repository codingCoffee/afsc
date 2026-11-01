<script setup lang="ts">
import type { MediaItem } from '@/types/api'
import { Check } from '@lucide/vue'
import MediaThumb from '@/components/media/MediaThumb.vue'

defineProps<{ items: MediaItem[], selected?: number[] }>()
const emit = defineEmits<{ pick: [item: MediaItem] }>()
</script>

<template>
  <ul class="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-6">
    <li v-for="m in items" :key="m.id">
      <button
        type="button"
        class="group relative block w-full overflow-hidden rounded-md border text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
        :class="selected?.includes(m.id) ? 'border-primary ring-2 ring-primary' : ''"
        :aria-pressed="selected ? selected.includes(m.id) : undefined"
        @click="emit('pick', m)"
      >
        <MediaThumb :item="m" class="aspect-square w-full" />
        <span class="block truncate border-t px-2 py-1.5 font-mono text-[11px] text-muted-foreground" :title="m.filename">
          {{ m.title || m.filename }}
        </span>
        <span v-if="selected?.includes(m.id)" class="absolute top-1.5 right-1.5 flex size-5 items-center justify-center rounded-full bg-primary text-primary-foreground">
          <Check class="size-3" />
        </span>
      </button>
    </li>
  </ul>
</template>
