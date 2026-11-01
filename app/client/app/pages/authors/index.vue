<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { Page } from '@/composables/useAuth'
import type { Author } from '@/types/api'
import { ExternalLink, Pencil, Plus, Trash2, UserRound } from '@lucide/vue'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import AuthorPicker from '@/components/taxonomy/AuthorPicker.vue'
import AuthorSheet from '@/components/taxonomy/AuthorSheet.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: 'Authors · AFSC' })

const API = '/admin/api/v1'
const PAGE_SIZE = 30
const { authFetch } = useAuth()
const page = ref(1)
const q = ref('')
const data = ref<Page<Author> | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<Author>>(`${API}/authors`, { query: { q: q.value, page: page.value, page_size: PAGE_SIZE } })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load authors.'
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

const sheetOpen = ref(false)
const editing = ref<Author | null>(null)
function openEdit(a: Author | null) {
  editing.value = a
  sheetOpen.value = true
}

// delete: authors with posts hand them to another author first
const deleting = ref<Author | null>(null)
const confirmOpen = ref(false)
const reassignOpen = ref(false)
const reassignTo = ref<Author | null>(null)

function startDelete(a: Author) {
  deleting.value = a
  reassignTo.value = null
  if (a.post_count > 0)
    reassignOpen.value = true
  else
    confirmOpen.value = true
}

async function remove(reassign?: number) {
  const a = deleting.value
  if (!a)
    return
  try {
    await authFetch(`${API}/authors/${a.id}`, { method: 'DELETE', query: reassign ? { reassign } : undefined })
    toast.success(`Deleted ${a.display_name}.`)
    reassignOpen.value = false
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not delete.')
  }
}

function actions(a: Author): Action[] {
  const list: Action[] = [
    { label: 'Edit', icon: Pencil, run: () => openEdit(a) },
    { label: 'Delete', icon: Trash2, danger: true, run: () => startDelete(a) },
  ]
  if (a.url)
    list.splice(1, 0, { label: 'View archive', icon: ExternalLink, run: () => window.open(a.url!, '_blank', 'noopener') })
  return list
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Authors" description="Bylines on posts. Link an author to a user account, or keep it as a guest author.">
      <template #actions>
        <Button size="sm" @click="openEdit(null)">
          <Plus />
          New author
        </Button>
      </template>
    </PageHeader>

    <Input v-model="q" placeholder="Search authors" class="max-w-xs" />
    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>Author</TableHead>
            <TableHead class="hidden md:table-cell">
              User
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
          <TableRow v-for="a in data?.items ?? []" :key="a.id">
            <TableCell>
              <button type="button" class="flex min-w-0 items-center gap-3 text-left" @click="openEdit(a)">
                <span class="flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-full border bg-muted/40">
                  <img v-if="a.avatar_url" :src="a.avatar_url" alt="" class="size-full object-cover">
                  <UserRound v-else class="size-4 text-muted-foreground" />
                </span>
                <span class="min-w-0">
                  <span class="block truncate font-medium hover:underline">{{ a.display_name }}</span>
                  <span class="block truncate font-mono text-[11px] text-muted-foreground">{{ a.slug }}</span>
                </span>
              </button>
            </TableCell>
            <TableCell class="hidden md:table-cell">
              <span v-if="a.user_name" class="text-sm">{{ a.user_name }}</span>
              <Badge v-else variant="outline" class="rounded-sm text-[10px] uppercase">
                guest
              </Badge>
            </TableCell>
            <TableCell class="text-right font-mono text-xs">
              {{ a.post_count }}
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(a)" :label="`Actions for ${a.display_name}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="4">
            {{ q ? 'No authors match.' : 'No authors yet.' }}
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <AuthorSheet v-model:open="sheetOpen" :author="editing" @saved="load" />

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Delete ${deleting?.display_name ?? ''}?`"
      description="The author has no posts. Their archive URL stops working."
      action="Delete author"
      @confirm="remove()"
    />

    <Dialog v-model:open="reassignOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Delete {{ deleting?.display_name }}</DialogTitle>
          <DialogDescription>
            They are credited on {{ deleting?.post_count }} posts. Pick the author who takes them over; the old archive redirects there.
          </DialogDescription>
        </DialogHeader>
        <p v-if="reassignTo" class="flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
          <span>Give posts to <strong>{{ reassignTo.display_name }}</strong></span>
          <Button variant="ghost" size="xs" @click="reassignTo = null">
            Change
          </Button>
        </p>
        <AuthorPicker v-else :exclude="deleting ? [deleting.id] : []" @pick="a => (reassignTo = a)" />
        <DialogFooter>
          <Button variant="destructive" :disabled="!reassignTo" @click="remove(reassignTo?.id)">
            Reassign and delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
