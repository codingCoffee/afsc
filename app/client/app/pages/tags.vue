<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { Page } from '@/composables/useAuth'
import type { Term } from '@/types/api'
import { ExternalLink, GitMerge, Pencil, Plus, Trash2 } from '@lucide/vue'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import MergeDialog from '@/components/taxonomy/MergeDialog.vue'
import TermDialog from '@/components/taxonomy/TermDialog.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { API } from '~/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: 'Tags · AFSC' })

const PAGE_SIZE = 50
const { authFetch } = useAuth()
const page = ref(1)
const q = ref('')
const data = ref<Page<Term> | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<Term>>(`${API}/terms`, { query: { taxonomy: 'tag', q: q.value, page: page.value, page_size: PAGE_SIZE } })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load tags.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 250)
})
watch(page, load)
load()

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
    <PageHeader title="Tags" description="Merge duplicates to keep tags tidy; the merged tag's archive redirects to the one you keep.">
      <template #actions>
        <Button size="sm" @click="openEdit(null)">
          <Plus />
          New tag
        </Button>
      </template>
    </PageHeader>

    <Input v-model="q" placeholder="Search tags" class="max-w-xs" />
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
          <TableRow v-for="t in data?.items ?? []" :key="t.id">
            <TableCell>
              <button type="button" class="max-w-[16rem] truncate text-left font-medium hover:underline sm:max-w-md" @click="openEdit(t)">
                {{ t.name }}
              </button>
            </TableCell>
            <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
              {{ t.slug }}
            </TableCell>
            <TableCell class="text-right font-mono text-xs">
              {{ t.count }}
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(t)" :label="`Actions for ${t.name}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="4">
            {{ q ? 'No tags match.' : 'No tags yet.' }}
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <TermDialog v-model:open="editOpen" taxonomy="tag" :term="editing" @saved="load" />
    <MergeDialog v-model:open="mergeOpen" :source="merging" @merged="load" />
    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Delete ${deleting?.name ?? ''}?`"
      description="Posts lose this tag and its archive URL stops working. Merging keeps the URL alive instead."
      action="Delete tag"
      @confirm="remove"
    />
  </div>
</template>
