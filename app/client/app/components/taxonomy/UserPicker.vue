<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import SearchPicker from '@/components/taxonomy/SearchPicker.vue'
import { API } from '~/composables/useAuth'

export interface UserRef { id: string, username: string, display_name: string, role: string }

const emit = defineEmits<{ pick: [user: UserRef] }>()
const { authFetch } = useAuth()

async function search(q: string) {
  return (await authFetch<Page<UserRef>>(`${API}/users`, { query: { q, page_size: 30 } })).items
}
</script>

<template>
  <SearchPicker
    :search="search"
    :label="(u: UserRef) => u.display_name || u.username"
    :hint="(u: UserRef) => `${u.username} · ${u.role}`"
    :item-key="(u: UserRef) => u.id"
    placeholder="Search users"
    @pick="(u: UserRef) => emit('pick', u)"
  />
</template>
