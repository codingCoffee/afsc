<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { Page } from '@/composables/useAuth'
import type { Term } from '@/types/api'
import { ExternalLink, GitMerge, Pencil, Plus, Trash2 } from '@lucide/vue'
import { computed, ref } from 'vue'
import { toast } from 'vue-sonner'
import MergeDialog from '@/components/taxonomy/MergeDialog.vue'
import TermDialog from '@/components/taxonomy/TermDialog.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { API } from '~/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: 'Categories · AFSC' })

const { authFetch } = useAuth()
const terms = ref<Term[]>([])
const loading = ref(false)
const error = ref('')
const q = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    terms.value = (await authFetch<Page<Term>>(`${API}/terms`, { query: { taxonomy: 'category', all: 1 } })).items
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load categories.'
  }
  finally {
    loading.value = false
  }
}
load()

// depth-first order with each category's depth, children sorted by name
const tree = computed(() => {
  const kids = new Map<number | null, Term[]>()
  const ids = new Set(terms.value.map(t => t.id))
  for (const t of terms.value) {
    const p = t.parent_id && ids.has(t.parent_id) ? t.parent_id : null
    kids.set(p, [...(kids.get(p) ?? []), t])
  }
  const out: { term: Term, depth: number }[] = []
  const walk = (p: number | null, depth: number) => {
    for (const t of (kids.get(p) ?? []).sort((a, b) => a.name.localeCompare(b.name))) {
      out.push({ term: t, depth })
      if (depth < 32)
        walk(t.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
})

const rows = computed(() => {
  const s = q.value.trim().toLowerCase()
  return s ? tree.value.filter(r => r.term.name.toLowerCase().includes(s) || r.term.slug.includes(s)) : tree.value
})

const editOpen = ref(false)
const editing = ref<Term | null>(null)
function openEdit(t: Term | null) {
  editing.value = t
  editOpen.value = true
}

const mergeOpen = ref(false)
const merging = ref<Term | null>(null)

const deleting = ref<Term | null>(null)
const confirmOpen = ref(false)
async function remove() {
  const t = deleting.value
  if (!t)
    return
  try {
    await authFetch(`${API}/terms/${t.id}`, { method: 'DELETE' })
    toast.success(`Deleted ${t.name}.`)
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not delete.')
  }
}

function actions(t: Term): Action[] {
  const list: Action[] = [
    { label: 'Edit', icon: Pencil, run: () => openEdit(t) },
    { label: 'Merge into…', icon: GitMerge, run: () => { merging.value = t; mergeOpen.value = true } },
    { label: 'Delete', icon: Trash2, danger: true, run: () => { deleting.value = t; confirmOpen.value = true } },
  ]
  if (t.url)
    list.splice(1, 0, { label: 'View archive', icon: ExternalLink, run: () => window.open(t.url!, '_blank', 'noopener') })
  return list
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Categories" description="Categories nest. Moving or renaming one redirects its old archive URL.">
      <template #actions>
        <Button size="sm" @click="openEdit(null)">
          <Plus />
          New category
        </Button>
      </template>
    </PageHeader>

    <Input v-model="q" placeholder="Filter categories" class="max-w-xs" />
    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>Name</TableHead>
            <TableHead class="hidden md:table-cell">
              Slug
            </TableHead>
            <TableHead class="text-right">
              Posts
            </TableHead>
            <TableHead class="w-12">
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in rows" :key="r.term.id">
            <TableCell>
              <div class="flex items-center gap-2" :style="{ paddingLeft: `${Math.min(r.depth, 6) * 1.25}rem` }">
                <span v-if="r.depth" class="text-muted-foreground">└</span>
                <button type="button" class="min-w-0 truncate text-left font-medium hover:underline" @click="openEdit(r.term)">
                  {{ r.term.name }}
                </button>
              </div>
            </TableCell>
            <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
              {{ r.term.slug }}
            </TableCell>
            <TableCell class="text-right font-mono text-xs">
              {{ r.term.count }}
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(r.term)" :label="`Actions for ${r.term.name}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="!loading && !rows.length" :colspan="4">
            {{ q ? 'No categories match.' : 'No categories yet.' }}
          </TableEmpty>
        </TableBody>
      </Table>
    </div>

    <TermDialog v-model:open="editOpen" taxonomy="category" :term="editing" :categories="tree" @saved="load" />
    <MergeDialog v-model:open="mergeOpen" :source="merging" @merged="load" />
    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Delete ${deleting?.name ?? ''}?`"
      description="Posts lose this category; its subcategories move up a level. The archive URL stops working."
      action="Delete category"
      @confirm="remove"
    />
  </div>
</template>
