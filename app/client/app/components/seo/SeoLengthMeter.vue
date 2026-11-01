<script setup lang="ts">
import { computed } from 'vue'
import { pixelWidth, seoLimits } from './seo'

const props = defineProps<{ text: string, kind: 'title' | 'description' }>()

const limit = computed(() => seoLimits[props.kind])
const chars = computed(() => [...props.text].length)
const px = computed(() => Math.round(pixelWidth(props.text, limit.value.font)))
const status = computed(() => {
  if (!chars.value)
    return 'problem'
  if (px.value > limit.value.px || chars.value > limit.value.max || chars.value < limit.value.min)
    return 'warn'
  return 'ok'
})
const bar = computed(() => ({ ok: 'bg-emerald-500', warn: 'bg-amber-500', problem: 'bg-destructive' })[status.value])
</script>

<template>
  <div class="grid gap-1" :aria-label="`${chars} characters, ${px} of ${limit.px} pixels`">
    <div class="h-1 overflow-hidden rounded-full bg-muted">
      <div class="h-full transition-[width]" :class="bar" :style="{ width: `${Math.min(100, (px / limit.px) * 100)}%` }" />
    </div>
    <div class="flex justify-between font-mono text-[10px] text-muted-foreground">
      <span>{{ chars }} chars · aim {{ limit.min }}–{{ limit.max }}</span>
      <span :class="{ 'text-amber-600 dark:text-amber-400': px > limit.px }">{{ px }} / {{ limit.px }} px</span>
    </div>
  </div>
</template>
