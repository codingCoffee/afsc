<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { TagChip } from '@/components/editor/TagPicker.vue'
import type { TrashChoice } from '@/components/editor/TrashDialog.vue'
import type { Page } from '~/composables/useAuth'
import type { Author, AuthorRef, PostListItem, PostStatus, PostType, Term } from '~/types/api'
import { Eye, Pencil, Plus, RotateCcw, SquarePen, Trash2, X } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import AuthorPicker from '@/components/editor/AuthorPicker.vue'
import CategoryPicker from '@/components/editor/CategoryPicker.vue'
import TagPicker from '@/components/editor/TagPicker.vue'
import TrashDialog from '@/components/editor/TrashDialog.vue'
import QuickEditSheet from '@/components/posts/QuickEditSheet.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { API } from '~/composables/useAuth'

const props = defineProps<{ type: PostType }>()

const PAGE_SIZE = 20
const base = props.type === 'post' ? '/posts' : '/pages'
const { authFetch, can } = useAuth()
const router = useRouter()

const tabs: { value: string, label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'draft', label: 'Drafts' },
  { value: 'pending', label: 'Pending' },
  { value: 'scheduled', label: 'Scheduled' },
  { value: 'published', label: 'Published' },
  { value: 'private', label: 'Private' },
  { value: 'trash', label: 'Trash' },
]

const status = ref('all')
const q = ref('')
const author = ref('')
const catFilter = ref('')
const tagFilter = ref<TagChip[]>([])
watch(catFilter, (v) => {
  if (v)
    tagFilter.value = []
})
watch(tagFilter, (v) => {
  if (v.length > 1)
    tagFilter.value = v.slice(-1)
  else if (v.length)
    catFilter.value = ''
})
const term = computed(() => catFilter.value || (tagFilter.value[0] ? String(tagFilter.value[0].id) : ''))
const from = ref('')
const to = ref('')
const sort = ref('-date')
const page = ref(1)
const data = ref<Page<PostListItem> | null>(null)
const loading = ref(false)
const error = ref('')
const selected = ref<number[]>([])

const authors = ref<Author[]>([])
const categories = ref<Term[]>([])
authFetch<Author[] | { items: Author[] }>(`${API}/authors`, { query: { all: 1 } })
  .then(res => authors.value = Array.isArray(res) ? res : res.items)
  .catch(() => {})
if (props.type === 'post') {
  authFetch<Term[] | { items: Term[] }>(`${API}/terms`, { query: { taxonomy: 'category', all: 1 } })
    .then(res => categories.value = Array.isArray(res) ? res : res.items)
    .catch(() => {})
}

// date inputs give local days; the API takes RFC 3339 instants
function dayStart(d: string, end = false) {
  if (!d)
    return undefined
  const [y, m, day] = d.split('-').map(Number)
  return new Date(y!, m! - 1, day! + (end ? 1 : 0)).toISOString()
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<PostListItem>>(`${API}/posts`, {
      query: {
        type: props.type,
        status: status.value === 'all' ? undefined : status.value,
        q: q.value || undefined,
        author: author.value || undefined,
        term: term.value || undefined,
        from: dayStart(from.value),
        to: dayStart(to.value, true),
        sort: sort.value,
        page: page.value,
        page_size: PAGE_SIZE,
      },
    })
    selected.value = selected.value.filter(id => data.value?.items.some(p => p.id === id))
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load the list.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([status, q, author, term, from, to, sort], () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    selected.value = []
    if (page.value === 1)
      load()
    else
      page.value = 1
  }, 250)
})
watch(page, load)
load()

const filtered = computed(() => !!(q.value || author.value || term.value || from.value || to.value))
function clearFilters() {
  q.value = ''
  author.value = ''
  catFilter.value = ''
  tagFilter.value = []
  from.value = ''
  to.value = ''
}

const items = computed(() => data.value?.items ?? [])
const editable = computed(() => items.value.filter(p => p.can_edit))
const allSelected = computed(() => editable.value.length > 0 && editable.value.every(p => selected.value.includes(p.id)))

function toggleAll(on: boolean | 'indeterminate') {
  selected.value = on === true ? editable.value.map(p => p.id) : []
}

function toggle(id: number, on: boolean | 'indeterminate') {
  selected.value = on === true ? [...selected.value, id] : selected.value.filter(x => x !== id)
}

interface BulkResult { id: number, ok: boolean, message?: string }

async function bulk(action: string, extra: Record<string, unknown> = {}, ids = selected.value) {
  try {
    const res = await authFetch<{ results: BulkResult[] }>(`${API}/posts/bulk`, { method: 'POST', body: { ids, action, ...extra } })
    const failed = res.results.filter(r => !r.ok)
    if (failed.length)
      toast.warning(`${res.results.length - failed.length} done, ${failed.length} failed`, { description: failed.slice(0, 5).map(f => `#${f.id}: ${f.message ?? 'failed'}`).join('\n') })
    else
      toast.success(`Done for ${res.results.length} item${res.results.length === 1 ? '' : 's'}.`)
    selected.value = []
    await load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'That didn\'t work.')
  }
}

async function single(fn: () => Promise<unknown>, done: string) {
  try {
    await fn()
    toast.success(done)
    await load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'That didn\'t work.')
  }
}

const trashOpen = ref(false)
const trashIds = ref<number[]>([])
const trashPublished = computed(() => items.value.some(p => trashIds.value.includes(p.id) && p.status === 'published'))
function askTrash(ids: number[]) {
  trashIds.value = ids
  trashOpen.value = true
}
function onTrash(choice: TrashChoice) {
  const extra = trashPublished.value ? { gone: choice.gone, ...(choice.target ? { target: choice.target } : {}) } : {}
  if (trashIds.value.length === 1)
    single(() => authFetch(`${API}/posts/${trashIds.value[0]}/trash`, { method: 'POST', body: extra }), 'Moved to trash.')
  else
    bulk('trash', extra, trashIds.value)
}

const deleting = ref<PostListItem | null>(null)
const deleteOpen = ref(false)

const termsOpen = ref(false)
const termsMode = ref<'set_terms' | 'add_terms'>('add_terms')
const bulkCats = ref<number[]>([])
const bulkTags = ref<TagChip[]>([])
function openTerms(mode: 'set_terms' | 'add_terms') {
  termsMode.value = mode
  bulkCats.value = []
  bulkTags.value = []
  termsOpen.value = true
}

const authorsOpen = ref(false)
const bulkAuthors = ref<AuthorRef[]>([])

const quickItem = ref<PostListItem | null>(null)
const quickOpen = ref(false)

function actions(p: PostListItem): Action[] {
  const list: Action[] = []
  if (p.status === 'trash') {
    if (p.can_edit)
      list.push({ label: 'Restore', icon: RotateCcw, run: () => single(() => authFetch(`${API}/posts/${p.id}/restore`, { method: 'POST' }), 'Restored as a draft.') })
    if (can('posts.edit_others'))
      list.push({ label: 'Delete permanently', icon: Trash2, danger: true, run: () => { deleting.value = p; deleteOpen.value = true } })
    return list
  }
  if (p.can_edit) {
    list.push({ label: 'Edit', icon: Pencil, run: () => router.push(`${base}/${p.id}`) })
    list.push({ label: 'Quick edit', icon: SquarePen, run: () => { quickItem.value = p; quickOpen.value = true } })
  }
  if (p.url)
    list.push({ label: 'View', icon: Eye, run: () => window.open(p.url!, '_blank', 'noopener') })
  if (p.can_edit)
    list.push({ label: 'Move to trash', icon: Trash2, danger: true, run: () => askTrash([p.id]) })
  return list
}

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const statusVariant = (s: PostStatus) => s === 'published' ? 'secondary' : s === 'trash' ? 'destructive' : 'outline'
</script>

<template>
  <div class="grid gap-6">
    <PageHeader :title="type === 'post' ? 'Posts' : 'Pages'">
      <template #actions>
        <Button size="sm" as-child>
          <NuxtLink :to="`${base}/new`">
            <Plus />
            New {{ type }}
          </NuxtLink>
        </Button>
      </template>
    </PageHeader>

    <Tabs v-model="status">
      <div class="overflow-x-auto">
        <TabsList>
          <TabsTrigger v-for="t in tabs" :key="t.value" :value="t.value" class="text-xs">
            {{ t.label }}
          </TabsTrigger>
        </TabsList>
      </div>
    </Tabs>

    <div class="flex flex-wrap items-center gap-2">
      <Input v-model="q" placeholder="Search" class="w-full sm:max-w-xs" />
      <NativeSelect v-model="author" aria-label="Author">
        <NativeSelectOption value="">
          All authors
        </NativeSelectOption>
        <NativeSelectOption v-for="a in authors" :key="a.id" :value="String(a.id)">
          {{ a.display_name }}
        </NativeSelectOption>
      </NativeSelect>
      <NativeSelect v-if="type === 'post'" v-model="catFilter" aria-label="Category">
        <NativeSelectOption value="">
          All categories
        </NativeSelectOption>
        <NativeSelectOption v-for="c in categories" :key="c.id" :value="String(c.id)">
          {{ c.name }}
        </NativeSelectOption>
      </NativeSelect>
      <TagPicker v-if="type === 'post'" v-model="tagFilter" no-create label="Tag" />
      <div class="flex items-center gap-1">
        <Input v-model="from" type="date" aria-label="From date" class="w-36" />
        <span class="text-muted-foreground">–</span>
        <Input v-model="to" type="date" aria-label="To date" class="w-36" />
      </div>
      <NativeSelect v-model="sort" aria-label="Sort">
        <NativeSelectOption value="-date">
          Newest
        </NativeSelectOption>
        <NativeSelectOption value="date">
          Oldest
        </NativeSelectOption>
        <NativeSelectOption value="modified">
          Recently modified
        </NativeSelectOption>
        <NativeSelectOption value="title">
          Title
        </NativeSelectOption>
      </NativeSelect>
      <Button v-if="filtered" variant="ghost" size="sm" @click="clearFilters">
        <X />
        Clear
      </Button>
    </div>

    <div v-if="selected.length" class="flex flex-wrap items-center gap-2 rounded-md border bg-muted/40 p-2 text-sm">
      <span class="px-1 font-mono text-xs">{{ selected.length }} selected</span>
      <template v-if="status === 'trash'">
        <Button size="sm" variant="outline" @click="bulk('restore')">
          Restore
        </Button>
      </template>
      <template v-else>
        <Button v-if="type === 'page' ? can('pages.edit') : can('posts.publish_own')" size="sm" variant="outline" @click="bulk('publish')">
          Publish
        </Button>
        <template v-if="type === 'post'">
          <Button size="sm" variant="outline" @click="openTerms('add_terms')">
            Add terms
          </Button>
          <Button size="sm" variant="outline" @click="openTerms('set_terms')">
            Set terms
          </Button>
        </template>
        <Button size="sm" variant="outline" @click="bulkAuthors = []; authorsOpen = true">
          Set authors
        </Button>
        <Button size="sm" variant="outline" class="text-destructive" @click="askTrash(selected)">
          Trash
        </Button>
      </template>
      <Button size="sm" variant="ghost" class="ml-auto" @click="selected = []">
        Clear
      </Button>
    </div>

    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead class="w-10">
              <Checkbox :model-value="allSelected" aria-label="Select all on this page" :disabled="!editable.length" @update:model-value="toggleAll" />
            </TableHead>
            <TableHead>Title</TableHead>
            <TableHead class="hidden md:table-cell">
              Authors
            </TableHead>
            <TableHead v-if="type === 'post'" class="hidden lg:table-cell">
              Categories
            </TableHead>
            <TableHead class="hidden md:table-cell">
              Date
            </TableHead>
            <TableHead class="w-12">
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="p in items" :key="p.id" :data-state="selected.includes(p.id) ? 'selected' : undefined">
            <TableCell>
              <Checkbox :model-value="selected.includes(p.id)" :disabled="!p.can_edit" :aria-label="`Select ${p.title || 'untitled'}`" @update:model-value="toggle(p.id, $event)" />
            </TableCell>
            <TableCell class="max-w-0 w-full">
              <NuxtLink v-if="p.can_edit && p.status !== 'trash'" :to="`${base}/${p.id}`" class="block truncate font-medium hover:underline">
                {{ p.title || '(untitled)' }}
              </NuxtLink>
              <span v-else class="block truncate font-medium">{{ p.title || '(untitled)' }}</span>
              <div class="flex flex-wrap items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
                <Badge v-if="status === 'all' || p.status !== status" :variant="statusVariant(p.status)" class="rounded-sm text-[10px] uppercase">
                  {{ p.status }}
                </Badge>
                <Badge v-if="p.sticky" variant="outline" class="rounded-sm text-[10px] uppercase">
                  sticky
                </Badge>
                <span class="truncate md:hidden">{{ p.authors.map(a => a.display_name).join(', ') }}</span>
              </div>
            </TableCell>
            <TableCell class="hidden text-muted-foreground md:table-cell">
              {{ p.authors.map(a => a.display_name).join(', ') }}
            </TableCell>
            <TableCell v-if="type === 'post'" class="hidden text-muted-foreground lg:table-cell">
              {{ p.categories.map(c => c.name).join(', ') }}
            </TableCell>
            <TableCell class="hidden font-mono text-xs whitespace-nowrap text-muted-foreground md:table-cell">
              {{ p.published_at ? fmt.format(new Date(p.published_at)) : `edited ${fmt.format(new Date(p.updated_at))}` }}
            </TableCell>
            <TableCell>
              <ActionMenu v-if="actions(p).length" :actions="actions(p)" :label="`Actions for ${p.title || 'untitled'}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !items.length" :colspan="type === 'post' ? 6 : 5">
            Nothing here{{ filtered ? ' matches the filters' : ' yet' }}.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <TrashDialog v-model:open="trashOpen" :count="trashIds.length" :published="trashPublished" @confirm="onTrash" />

    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="`Delete “${deleting?.title || 'untitled'}” permanently?`"
      description="The post, its revisions and its term links are removed. This can't be undone."
      action="Delete permanently"
      @confirm="deleting && single(() => authFetch(`${API}/posts/${deleting!.id}`, { method: 'DELETE' }), 'Deleted.')"
    />

    <Dialog v-model:open="termsOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ termsMode === 'set_terms' ? 'Set terms' : 'Add terms' }}</DialogTitle>
          <DialogDescription>
            {{ termsMode === 'set_terms' ? 'Replaces the categories and tags of the selected posts.' : 'Adds these to the selected posts and keeps what they have.' }}
          </DialogDescription>
        </DialogHeader>
        <div class="grid gap-4">
          <div class="grid gap-2">
            <div class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
              Categories
            </div>
            <CategoryPicker v-model:ids="bulkCats" no-primary />
          </div>
          <div class="grid gap-2">
            <div class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
              Tags
            </div>
            <TagPicker v-model="bulkTags" />
          </div>
        </div>
        <DialogFooter>
          <Button
            :disabled="termsMode === 'add_terms' && !bulkCats.length && !bulkTags.length"
            @click="termsOpen = false; bulk(termsMode, { term_ids: [...bulkCats, ...bulkTags.map(t => t.id)] })"
          >
            Apply to {{ selected.length }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="authorsOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Set authors</DialogTitle>
          <DialogDescription>Replaces the authors of the selected items, in this order.</DialogDescription>
        </DialogHeader>
        <AuthorPicker v-model="bulkAuthors" />
        <DialogFooter>
          <Button :disabled="!bulkAuthors.length" @click="authorsOpen = false; bulk('set_authors', { author_ids: bulkAuthors.map(a => a.id) })">
            Apply to {{ selected.length }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <QuickEditSheet v-model:open="quickOpen" :item="quickItem" @saved="load(); toast.success('Saved.')" />
  </div>
</template>
