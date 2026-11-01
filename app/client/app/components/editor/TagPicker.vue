<script setup lang="ts">
import type { Term } from '~/types/api'
import { Plus, X } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { API } from '~/composables/useAuth'

export interface TagChip { id: number, name: string }

const tags = defineModel<TagChip[]>({ required: true })
const props = defineProps<{ noCreate?: boolean, label?: string }>()
const { authFetch } = useAuth()
const open = ref(false)
const q = ref('')
const found = ref<Term[]>([])

let timer: ReturnType<typeof setTimeout> | undefined
watch([q, open], () => {
  if (!open.value)
    return
  clearTimeout(timer)
  timer = setTimeout(async () => {
    const res = await authFetch<{ items: Term[] }>(`${API}/terms`, { query: { taxonomy: 'tag', q: q.value, page_size: 20 } }).catch(() => ({ items: [] }))
    found.value = res.items
  }, 200)
})

const options = computed(() => found.value.filter(t => !tags.value.some(s => s.id === t.id)))
const canCreate = computed(() => !props.noCreate && q.value.trim() && !found.value.some(t => t.name.toLowerCase() === q.value.trim().toLowerCase()))

function add(t: TagChip) {
  tags.value = [...tags.value, { id: t.id, name: t.name }]
  q.value = ''
}

async function create() {
  try {
    const t = await authFetch<Term>(`${API}/terms`, { method: 'POST', body: { taxonomy: 'tag', name: q.value.trim(), slug: '', description: '', parent_id: null } })
    add(t)
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not create the tag.')
  }
}
</script>

<template>
  <div class="grid gap-2">
    <div v-if="tags.length" class="flex flex-wrap gap-1">
      <Badge v-for="t in tags" :key="t.id" variant="secondary" class="gap-1 pr-1">
        {{ t.name }}
        <button type="button" class="rounded-sm opacity-60 hover:opacity-100" :aria-label="`Remove ${t.name}`" @click="tags = tags.filter(x => x.id !== t.id)">
          <X class="size-3" />
        </button>
      </Badge>
    </div>
    <Popover v-model:open="open">
      <PopoverTrigger as-child>
        <Button variant="outline" size="sm" class="justify-self-start">
          <Plus />
          {{ label ?? 'Add tag' }}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" class="w-64 p-0">
        <Command>
          <CommandInput placeholder="Search or create" @update:model-value="q = String($event)" />
          <CommandList>
            <CommandEmpty v-if="!canCreate">
              No tags found.
            </CommandEmpty>
            <CommandItem v-for="t in options" :key="t.id" :value="t.name" @select="add(t)">
              {{ t.name }}
            </CommandItem>
            <CommandItem v-if="canCreate" :value="`${q} (create)`" @select="create">
              <Plus />
              Create “{{ q.trim() }}”
            </CommandItem>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  </div>
</template>
