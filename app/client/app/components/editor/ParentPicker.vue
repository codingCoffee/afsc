<script setup lang="ts">
import type { Page } from '~/composables/useAuth'
import type { PostDetail, PostListItem } from '~/types/api'
import { ChevronsUpDown } from '@lucide/vue'
import { ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { API } from '~/composables/useAuth'

const parent = defineModel<number | null>({ required: true })
const props = defineProps<{ exclude: number | null }>()
const { authFetch } = useAuth()
const open = ref(false)
const q = ref('')
const found = ref<PostListItem[]>([])
const label = ref('')

watch(parent, async (id) => {
  if (id == null) {
    label.value = ''
    return
  }
  if (found.value.some(p => p.id === id))
    return
  label.value = await authFetch<PostDetail>(`${API}/posts/${id}`).then(p => p.title).catch(() => `#${id}`)
}, { immediate: true })

let timer: ReturnType<typeof setTimeout> | undefined
watch([q, open], () => {
  if (!open.value)
    return
  clearTimeout(timer)
  timer = setTimeout(async () => {
    const res = await authFetch<Page<PostListItem>>(`${API}/posts`, { query: { type: 'page', q: q.value, page_size: 20 } }).catch(() => null)
    found.value = (res?.items ?? []).filter(p => p.id !== props.exclude)
  }, 200)
})

function pick(p: PostListItem | null) {
  parent.value = p?.id ?? null
  label.value = p?.title ?? ''
  open.value = false
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button variant="outline" size="sm" class="w-full justify-between font-normal">
        <span class="truncate">{{ parent == null ? 'No parent (top level)' : (label || '…') }}</span>
        <ChevronsUpDown class="opacity-50" />
      </Button>
    </PopoverTrigger>
    <PopoverContent align="start" class="w-72 p-0">
      <Command>
        <CommandInput placeholder="Search pages" @update:model-value="q = String($event)" />
        <CommandList>
          <CommandEmpty>No pages found.</CommandEmpty>
          <CommandItem value="(no parent)" @select="pick(null)">
            No parent (top level)
          </CommandItem>
          <CommandItem v-for="p in found" :key="p.id" :value="`${p.title} ${p.slug} ${p.id}`" @select="pick(p)">
            {{ p.title || '(untitled)' }}
          </CommandItem>
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>
</template>
