<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { Page } from '@/composables/useAuth'
import type { NotFoundRow } from '@/types/api'
import { ArrowRightLeft, EyeOff } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import SeoRedirectDialog from '@/components/seo/SeoRedirectDialog.vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { API, errorData } from '@/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: '404 log · AFSC' })

const PAGE_SIZE = 50
const { authFetch } = useAuth()

const page = ref(1)
const q = ref('')
const sort = ref('hits')
const data = ref<Page<NotFoundRow> | null>(null)
const loading = ref(false)
const error = ref('')
const selected = ref<Set<string>>(new Set())

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<NotFoundRow>>(`${API}/not-found`, { query: { page: page.value, page_size: PAGE_SIZE, q: q.value, sort: sort.value } })
    selected.value = new Set()
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load the 404 log.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([q, sort], () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 250)
})
watch(page, load)
load()

const paths = computed(() => data.value?.items.map(r => r.path) ?? [])
const allSelected = computed(() => paths.value.length > 0 && paths.value.every(p => selected.value.has(p)))

function toggle(path: string, on: boolean) {
  const next = new Set(selected.value)
  if (on)
    next.add(path)
  else
    next.delete(path)
  selected.value = next
}

function toggleAll(on: boolean) {
  selected.value = on ? new Set(paths.value) : new Set()
}

async function dismiss(list: string[], quiet = false) {
  try {
    const res = await authFetch<{ dismissed: number }>(`${API}/not-found/dismiss`, { method: 'POST', body: { paths: list } })
    if (!quiet)
      toast.success(`Dismissed ${res.dismissed}. They come back if they're hit again.`)
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not dismiss.')
  }
}

const dialogOpen = ref(false)
const source = ref('')
function redirectFrom(path: string) {
  source.value = path
  dialogOpen.value = true
}

function actions(r: NotFoundRow): Action[] {
  return [
    { label: 'Create redirect', icon: ArrowRightLeft, run: () => redirectFrom(r.path) },
    { label: 'Dismiss', icon: EyeOff, run: () => dismiss([r.path]) },
  ]
}

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="404 log" description="Paths visitors asked for that don't exist, most hits first. Noise patterns and retention are in SEO settings.">
      <template #actions>
        <Button variant="outline" size="sm" :disabled="!selected.size" @click="dismiss([...selected])">
          <EyeOff />
          Dismiss {{ selected.size || '' }}
        </Button>
      </template>
    </PageHeader>

    <div class="flex flex-wrap gap-2">
      <Input v-model="q" placeholder="Search paths" class="max-w-xs" />
      <NativeSelect v-model="sort" aria-label="Sort">
        <NativeSelectOption value="hits">
          Most hits
        </NativeSelectOption>
        <NativeSelectOption value="last_seen">
          Most recent
        </NativeSelectOption>
      </NativeSelect>
    </div>

    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead class="w-10">
              <Checkbox :model-value="allSelected" aria-label="Select all" @update:model-value="toggleAll($event === true)" />
            </TableHead>
            <TableHead>Path</TableHead>
            <TableHead class="text-right">
              Hits
            </TableHead>
            <TableHead class="hidden md:table-cell">
              Last seen
            </TableHead>
            <TableHead class="hidden lg:table-cell">
              Referrer
            </TableHead>
            <TableHead class="w-12">
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in data?.items ?? []" :key="r.path">
            <TableCell>
              <Checkbox :model-value="selected.has(r.path)" :aria-label="`Select ${r.path}`" @update:model-value="toggle(r.path, $event === true)" />
            </TableCell>
            <TableCell class="max-w-[14rem] sm:max-w-md">
              <div class="truncate font-mono text-xs" :title="r.path">
                {{ r.path }}
              </div>
            </TableCell>
            <TableCell class="text-right font-mono text-xs">
              {{ r.hits }}
            </TableCell>
            <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
              {{ fmt.format(new Date(r.last_seen)) }}
            </TableCell>
            <TableCell class="hidden max-w-xs lg:table-cell">
              <div class="truncate text-xs text-muted-foreground" :title="r.last_referrer">
                {{ r.last_referrer || '—' }}
              </div>
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(r)" :label="`Actions for ${r.path}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="6">
            No 404s logged.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <SeoRedirectDialog v-model:open="dialogOpen" :source="source" @saved="dismiss([source], true)" />
  </div>
</template>
