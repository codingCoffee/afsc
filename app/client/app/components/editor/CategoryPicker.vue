<script setup lang="ts">
import type { Term } from '~/types/api'
import { computed, ref, watch } from 'vue'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { API } from '~/composables/useAuth'

const ids = defineModel<number[]>('ids', { required: true })
const primary = defineModel<number | null>('primary', { default: null })
const props = defineProps<{ noPrimary?: boolean }>()
const { authFetch } = useAuth()
const terms = ref<Term[]>([])

authFetch<Term[] | { items: Term[] }>(`${API}/terms`, { query: { taxonomy: 'category', all: 1 } })
  .then(res => terms.value = Array.isArray(res) ? res : res.items)
  .catch(() => {})

const tree = computed(() => {
  const kids = new Map<number | null, Term[]>()
  for (const t of terms.value) {
    const p = terms.value.some(x => x.id === t.parent_id) ? t.parent_id : null
    kids.set(p, [...(kids.get(p) ?? []), t])
  }
  const out: { term: Term, depth: number }[] = []
  const walk = (parent: number | null, depth: number) => {
    for (const t of (kids.get(parent) ?? []).sort((a, b) => a.name.localeCompare(b.name))) {
      out.push({ term: t, depth })
      walk(t.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
})

watch(ids, (list) => {
  if (primary.value == null || !list.includes(primary.value))
    primary.value = list[0] ?? null
}, { immediate: true })

function toggle(id: number, on: boolean | 'indeterminate') {
  ids.value = on === true ? [...ids.value, id] : ids.value.filter(x => x !== id)
}
</script>

<template>
  <RadioGroup
    :model-value="primary == null ? undefined : String(primary)"
    class="grid max-h-64 gap-1 overflow-y-auto"
    @update:model-value="primary = Number($event)"
  >
    <div v-for="{ term, depth } in tree" :key="term.id" class="flex items-center gap-2 text-sm" :style="{ paddingLeft: `${depth}rem` }">
      <Checkbox :id="`cat-${term.id}`" :model-value="ids.includes(term.id)" @update:model-value="toggle(term.id, $event)" />
      <Label :for="`cat-${term.id}`" class="flex-1 truncate font-normal">{{ term.name }}</Label>
      <RadioGroupItem v-if="!props.noPrimary && ids.length > 1 && ids.includes(term.id)" :value="String(term.id)" :aria-label="`Make ${term.name} the primary category`" title="Primary" />
    </div>
    <p v-if="!tree.length" class="text-xs text-muted-foreground">
      No categories yet.
    </p>
  </RadioGroup>
  <p v-if="!props.noPrimary && ids.length > 1" class="mt-1 text-[11px] text-muted-foreground">
    The radio marks the primary category (used in URLs and breadcrumbs).
  </p>
</template>
