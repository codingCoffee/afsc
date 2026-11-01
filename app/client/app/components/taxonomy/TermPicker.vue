<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { Term } from '@/types/api'
import SearchPicker from '@/components/taxonomy/SearchPicker.vue'

const props = defineProps<{ taxonomy?: 'category' | 'tag', exclude?: number[], placeholder?: string }>()
const emit = defineEmits<{ pick: [term: Term] }>()
const { authFetch } = useAuth()

async function search(q: string) {
  const res = await authFetch<Page<Term>>('/admin/api/v1/terms', { query: { taxonomy: props.taxonomy, q, page_size: 30 } })
  return res.items.filter(t => !props.exclude?.includes(t.id))
}
</script>

<template>
  <SearchPicker
    :search="search"
    :label="(t: Term) => t.name"
    :hint="(t: Term) => `${taxonomy ? '' : `${t.taxonomy} · `}${t.count} posts`"
    :item-key="(t: Term) => t.id"
    :placeholder="placeholder ?? 'Search terms'"
    @pick="(t: Term) => emit('pick', t)"
  />
</template>
