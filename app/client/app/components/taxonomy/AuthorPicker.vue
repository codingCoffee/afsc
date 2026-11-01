<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { Author } from '@/types/api'
import SearchPicker from '@/components/taxonomy/SearchPicker.vue'

const props = defineProps<{ exclude?: number[], placeholder?: string }>()
const emit = defineEmits<{ pick: [author: Author] }>()
const { authFetch } = useAuth()

async function search(q: string) {
  const res = await authFetch<Page<Author>>('/admin/api/v1/authors', { query: { q, page_size: 30 } })
  return res.items.filter(a => !props.exclude?.includes(a.id))
}
</script>

<template>
  <SearchPicker
    :search="search"
    :label="(a: Author) => a.display_name"
    :hint="(a: Author) => `${a.post_count} posts`"
    :item-key="(a: Author) => a.id"
    :placeholder="placeholder ?? 'Search authors'"
    @pick="(a: Author) => emit('pick', a)"
  />
</template>
