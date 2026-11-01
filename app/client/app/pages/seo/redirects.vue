<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { Page } from '@/composables/useAuth'
import type { CsvLineError, Redirect, RedirectImportReport } from '@/types/api'
import { Download, Pencil, Plus, Trash2, Upload } from '@lucide/vue'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import SeoRedirectDialog from '@/components/seo/SeoRedirectDialog.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { API, errorData } from '@/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: 'Redirects · AFSC' })

const PAGE_SIZE = 50
const { authFetch } = useAuth()

const page = ref(1)
const q = ref('')
const match = ref('')
const origin = ref('')
const sort = ref('created')
const data = ref<Page<Redirect> | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<Redirect>>(`${API}/redirects`, {
      query: { page: page.value, page_size: PAGE_SIZE, q: q.value, match: match.value, origin: origin.value, sort: sort.value },
    })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load redirects.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([q, match, origin, sort], () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 250)
})
watch(page, load)
load()

const dialogOpen = ref(false)
const editing = ref<Redirect | null>(null)
function openEditor(r: Redirect | null) {
  editing.value = r
  dialogOpen.value = true
}

const deleting = ref<Redirect | null>(null)
const confirmOpen = ref(false)
async function remove() {
  const r = deleting.value
  if (!r)
    return
  try {
    await authFetch(`${API}/redirects/${r.id}`, { method: 'DELETE' })
    toast.success('Redirect deleted.')
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not delete the redirect.')
  }
}

function actions(r: Redirect): Action[] {
  return [
    { label: 'Edit', icon: Pencil, run: () => openEditor(r) },
    { label: 'Delete', icon: Trash2, danger: true, run: () => { deleting.value = r; confirmOpen.value = true } },
  ]
}

// CSV import is all or nothing; a 422 lists every bad line
const fileInput = ref<HTMLInputElement | null>(null)
const importFile = ref<File | null>(null)
const importErrors = ref<CsvLineError[]>([])
const importOpen = ref(false)
const importing = ref(false)

async function importCSV(file: File, flatten = false) {
  importing.value = true
  importFile.value = file
  try {
    const rep = await authFetch<RedirectImportReport>(`${API}/redirects/import`, {
      method: 'POST',
      body: file,
      headers: { 'Content-Type': 'text/csv' },
      query: flatten ? { flatten: 1 } : undefined,
    })
    importOpen.value = false
    toast.success(`Imported: ${rep.created} created, ${rep.updated} updated.`)
    if (rep.warnings.length)
      toast.warning(`${rep.warnings.length} warnings, for example line ${rep.warnings[0]!.line}: ${rep.warnings[0]!.message}`)
    load()
  }
  catch (e) {
    const data = errorData<RedirectImportReport>(e)
    importErrors.value = data.errors ?? [{ line: 0, message: data.message ?? 'Could not import the file.' }]
    importOpen.value = true
  }
  finally {
    importing.value = false
    if (fileInput.value)
      fileInput.value.value = ''
  }
}

function onFile(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (f)
    importCSV(f)
}

async function exportCSV() {
  try {
    const blob = await authFetch<Blob>(`${API}/redirects/export`, { responseType: 'blob' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'redirects.csv'
    a.click()
    URL.revokeObjectURL(url)
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not export.')
  }
}

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Redirects" description="Old URLs that send visitors and search engines somewhere else. Chains are caught on save.">
      <template #actions>
        <Button variant="outline" size="sm" :disabled="importing" @click="fileInput?.click()">
          <Upload />
          {{ importing ? 'Importing' : 'Import CSV' }}
        </Button>
        <input ref="fileInput" type="file" accept=".csv,text/csv" class="hidden" @change="onFile">
        <Button variant="outline" size="sm" @click="exportCSV">
          <Download />
          Export CSV
        </Button>
        <Button size="sm" @click="openEditor(null)">
          <Plus />
          New redirect
        </Button>
      </template>
    </PageHeader>

    <div class="flex flex-wrap gap-2">
      <Input v-model="q" placeholder="Search source, target or note" class="max-w-xs" />
      <NativeSelect v-model="match" aria-label="Match">
        <NativeSelectOption value="">
          Any match
        </NativeSelectOption>
        <NativeSelectOption v-for="m in ['exact', 'prefix', 'regex']" :key="m" :value="m">
          {{ m }}
        </NativeSelectOption>
      </NativeSelect>
      <NativeSelect v-model="origin" aria-label="Origin">
        <NativeSelectOption value="">
          Any origin
        </NativeSelectOption>
        <NativeSelectOption v-for="o in ['manual', 'slug_change', 'import']" :key="o" :value="o">
          {{ o.replace('_', ' ') }}
        </NativeSelectOption>
      </NativeSelect>
      <NativeSelect v-model="sort" aria-label="Sort">
        <NativeSelectOption value="created">
          Newest
        </NativeSelectOption>
        <NativeSelectOption value="hits">
          Most hits
        </NativeSelectOption>
        <NativeSelectOption value="source">
          Source
        </NativeSelectOption>
      </NativeSelect>
    </div>

    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>Source</TableHead>
            <TableHead class="hidden md:table-cell">
              Target
            </TableHead>
            <TableHead>Status</TableHead>
            <TableHead class="hidden text-right md:table-cell">
              Hits
            </TableHead>
            <TableHead class="hidden lg:table-cell">
              Origin
            </TableHead>
            <TableHead class="w-12">
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in data?.items ?? []" :key="r.id">
            <TableCell class="max-w-[16rem] sm:max-w-sm">
              <div class="truncate font-mono text-xs" :title="r.source">
                {{ r.source }}
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                <Badge v-if="r.match !== 'exact'" variant="secondary" class="rounded-sm text-[10px] uppercase">
                  {{ r.match }}
                </Badge>
                <span class="truncate font-mono text-[11px] text-muted-foreground md:hidden">→ {{ r.target ?? 'gone' }}</span>
              </div>
            </TableCell>
            <TableCell class="hidden max-w-xs md:table-cell">
              <div class="truncate font-mono text-xs" :title="r.target ?? ''">
                {{ r.target ?? '—' }}
              </div>
              <div v-if="r.note" class="truncate text-xs text-muted-foreground">
                {{ r.note }}
              </div>
            </TableCell>
            <TableCell class="font-mono text-xs">
              {{ r.status }}
            </TableCell>
            <TableCell class="hidden text-right font-mono text-xs md:table-cell">
              {{ r.hits }}
              <div v-if="r.last_hit_at" class="text-[10px] text-muted-foreground">
                {{ fmt.format(new Date(r.last_hit_at)) }}
              </div>
            </TableCell>
            <TableCell class="hidden text-xs text-muted-foreground lg:table-cell">
              {{ r.origin.replace('_', ' ') }}
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(r)" :label="`Actions for ${r.source}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="6">
            No redirects match.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <SeoRedirectDialog v-model:open="dialogOpen" :redirect="editing" @saved="load" />

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Delete redirect?`"
      :description="`${deleting?.source ?? ''} will answer normally again, usually with a 404.`"
      action="Delete"
      @confirm="remove"
    />

    <Dialog v-model:open="importOpen">
      <DialogContent class="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Nothing was imported</DialogTitle>
          <DialogDescription>Fix these lines and import the file again. Chains can be flattened automatically.</DialogDescription>
        </DialogHeader>
        <ul class="grid gap-1 text-sm">
          <li v-for="(e, i) in importErrors" :key="i" class="flex gap-2">
            <span v-if="e.line" class="shrink-0 font-mono text-xs text-muted-foreground">line {{ e.line }}</span>
            <span><span v-if="e.field" class="font-mono text-xs">{{ e.field }}: </span>{{ e.message }}</span>
          </li>
        </ul>
        <DialogFooter>
          <Button variant="outline" @click="importOpen = false">
            Close
          </Button>
          <Button v-if="importFile" :disabled="importing" @click="importCSV(importFile, true)">
            Import, flattening chains
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
