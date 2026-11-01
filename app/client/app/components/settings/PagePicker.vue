<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { PostDetail, PostListItem } from '@/types/api'
import { ChevronsUpDown, X } from '@lucide/vue'
import { ref, watch } from 'vue'
import { API } from '@/composables/useAuth'
import { Button } from '@/components/ui/button'
import { Command, CommandItem, CommandList } from '@/components/ui/command'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

const props = defineProps<{ id?: string, invalid?: boolean }>()
const model = defineModel<number | null>({ default: null })

const { authFetch } = useAuth()
const open = ref(false)
const q = ref('')
const results = ref<PostListItem[]>([])
const loading = ref(false)
const title = ref('')

async function search() {
  loading.value = true
  try {
    results.value = (await authFetch<Page<PostListItem>>(`${API}/posts`, { query: { type: 'page', q: q.value, page_size: 20 } })).items
  }
  catch {
    results.value = []
  }
  finally {
    loading.value = false
  }
}

async function loadTitle(id: number | null) {
  if (!id) {
    title.value = ''
    return
  }
  if (results.value.some(p => p.id === id))
    return
  try {
    title.value = (await authFetch<PostDetail>(`${API}/posts/${id}`)).title
  }
  catch {
    title.value = `Page #${id}`
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(search, 250)
})
watch(open, (o) => {
  if (o && !results.value.length)
    search()
})
watch(model, loadTitle, { immediate: true })

function pick(p: PostListItem) {
  title.value = p.title
  model.value = p.id
  open.value = false
}
</script>

<template>
  <div class="flex items-center gap-2">
    <Popover v-model:open="open">
      <PopoverTrigger as-child>
        <Button :id="props.id" type="button" variant="outline" class="w-full justify-between font-normal sm:w-80" :aria-invalid="invalid">
          <span class="truncate" :class="{ 'text-muted-foreground': !model }">{{ model ? title : 'None' }}</span>
          <ChevronsUpDown class="opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" class="w-(--reka-popover-trigger-width) min-w-64 p-0">
        <Command>
          <div class="border-b p-2">
            <Input v-model="q" placeholder="Search pages" class="h-8" />
          </div>
          <CommandList>
            <p v-if="!results.length" class="px-3 py-6 text-center text-sm text-muted-foreground">
              {{ loading ? 'Searching…' : 'No pages found.' }}
            </p>
            <CommandItem v-for="p in results" :key="p.id" :value="String(p.id)" @select="pick(p)">
              <span class="truncate">{{ p.title || p.slug }}</span>
              <span class="ml-auto font-mono text-[10px] text-muted-foreground uppercase">{{ p.status }}</span>
            </CommandItem>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
    <Button v-if="model" type="button" variant="ghost" size="icon-sm" aria-label="Clear" @click="model = null">
      <X />
    </Button>
  </div>
</template>
