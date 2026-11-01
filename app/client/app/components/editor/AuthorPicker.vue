<script setup lang="ts">
import type { Author, AuthorRef } from '~/types/api'
import { ArrowDown, ArrowUp, Plus, X } from '@lucide/vue'
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { API } from '~/composables/useAuth'

const authors = defineModel<AuthorRef[]>({ required: true })
const { authFetch } = useAuth()
const all = ref<Author[]>([])
const open = ref(false)

async function load() {
  if (all.value.length)
    return
  const res = await authFetch<Author[] | { items: Author[] }>(`${API}/authors`, { query: { all: 1 } }).catch(() => [])
  all.value = Array.isArray(res) ? res : res.items
}

const available = computed(() => all.value.filter(a => !authors.value.some(s => s.id === a.id)))

function add(a: Author) {
  authors.value = [...authors.value, { id: a.id, display_name: a.display_name, slug: a.slug }]
  open.value = false
}

function move(i: number, d: -1 | 1) {
  const list = [...authors.value]
  const [a] = list.splice(i, 1)
  list.splice(i + d, 0, a!)
  authors.value = list
}
</script>

<template>
  <div class="grid gap-2">
    <ol v-if="authors.length" class="grid gap-1">
      <li v-for="(a, i) in authors" :key="a.id" class="flex items-center gap-1 rounded-md border px-2 py-1 text-sm">
        <span class="flex-1 truncate">{{ a.display_name }}</span>
        <Button variant="ghost" size="icon-sm" :disabled="i === 0" :aria-label="`Move ${a.display_name} up`" @click="move(i, -1)">
          <ArrowUp />
        </Button>
        <Button variant="ghost" size="icon-sm" :disabled="i === authors.length - 1" :aria-label="`Move ${a.display_name} down`" @click="move(i, 1)">
          <ArrowDown />
        </Button>
        <Button variant="ghost" size="icon-sm" :aria-label="`Remove ${a.display_name}`" @click="authors = authors.filter(x => x.id !== a.id)">
          <X />
        </Button>
      </li>
    </ol>
    <p v-else class="text-xs text-muted-foreground">
      You, unless you pick someone.
    </p>
    <Popover v-model:open="open" @update:open="(v: boolean) => v && load()">
      <PopoverTrigger as-child>
        <Button variant="outline" size="sm" class="justify-self-start">
          <Plus />
          Add author
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" class="w-64 p-0">
        <Command>
          <CommandInput placeholder="Search authors" />
          <CommandList>
            <CommandEmpty>No authors found.</CommandEmpty>
            <CommandItem v-for="a in available" :key="a.id" :value="`${a.display_name} ${a.slug}`" @select="add(a)">
              {{ a.display_name }}
            </CommandItem>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  </div>
</template>
