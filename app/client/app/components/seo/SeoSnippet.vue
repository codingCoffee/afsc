<script setup lang="ts">
import { Monitor, Smartphone } from '@lucide/vue'
import { computed, ref } from 'vue'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

const props = defineProps<{ title: string, description: string, url: string, siteName?: string }>()
const device = ref<'desktop' | 'mobile'>('desktop')

const crumbs = computed(() => {
  try {
    const u = new URL(props.url)
    return [u.host, ...u.pathname.split('/').filter(Boolean).map(decodeURIComponent)].join(' › ')
  }
  catch {
    return props.url
  }
})
</script>

<template>
  <div class="grid gap-2">
    <div class="flex items-center justify-between gap-2">
      <span class="font-mono text-[11px] tracking-widest text-muted-foreground uppercase">Search preview</span>
      <ToggleGroup
        :model-value="device"
        type="single"
        variant="outline"
        size="sm"
        aria-label="Preview device"
        @update:model-value="(v) => v && (device = v as 'desktop' | 'mobile')"
      >
        <ToggleGroupItem value="desktop" aria-label="Desktop">
          <Monitor />
        </ToggleGroupItem>
        <ToggleGroupItem value="mobile" aria-label="Mobile">
          <Smartphone />
        </ToggleGroupItem>
      </ToggleGroup>
    </div>
    <div class="overflow-hidden rounded-md border bg-white p-4 font-[Arial,sans-serif] text-[#202124]">
      <div :class="device === 'desktop' ? 'max-w-[600px]' : 'max-w-[360px]'">
        <div class="flex items-center gap-2">
          <span class="grid size-6 shrink-0 place-items-center rounded-full bg-[#f1f3f4] text-[11px] font-bold text-[#5f6368]">
            {{ (siteName || crumbs).slice(0, 1).toUpperCase() }}
          </span>
          <div class="min-w-0 leading-tight">
            <div class="truncate text-sm">{{ siteName }}</div>
            <div class="truncate text-xs text-[#4d5156]">{{ crumbs }}</div>
          </div>
        </div>
        <div
          class="mt-1.5 text-[#1a0dab]"
          :class="device === 'desktop' ? 'truncate text-xl leading-7' : 'line-clamp-2 text-lg leading-6'"
        >
          {{ title }}
        </div>
        <p class="mt-1 text-sm leading-[1.58] text-[#4d5156]" :class="device === 'desktop' ? 'line-clamp-2' : 'line-clamp-3'">
          {{ description || 'Search engines pick text from the page when there is no meta description.' }}
        </p>
      </div>
    </div>
  </div>
</template>
