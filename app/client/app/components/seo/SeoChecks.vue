<script setup lang="ts">
import type { SeoCheck } from '@/types/api'
import { CircleAlert, CircleCheck, CircleX } from '@lucide/vue'
import { computed } from 'vue'

const props = defineProps<{ checks: SeoCheck[] }>()
const order = { problem: 0, warn: 1, ok: 2 }
const sorted = computed(() => [...props.checks].sort((a, b) => order[a.status] - order[b.status]))
const icons = { ok: CircleCheck, warn: CircleAlert, problem: CircleX }
const colors = { ok: 'text-emerald-600 dark:text-emerald-400', warn: 'text-amber-600 dark:text-amber-400', problem: 'text-destructive' }
</script>

<template>
  <ul class="grid gap-1.5">
    <li v-for="c in sorted" :key="c.id" class="flex items-start gap-2 text-sm">
      <component :is="icons[c.status]" class="mt-0.5 size-4 shrink-0" :class="colors[c.status]" :aria-label="c.status" />
      <span>{{ c.message }}</span>
    </li>
  </ul>
</template>
