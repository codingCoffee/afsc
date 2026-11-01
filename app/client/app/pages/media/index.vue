<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { MediaItem } from '@/types/api'
import { History, LayoutGrid, List, Upload } from '@lucide/vue'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import MediaDetails from '@/components/media/MediaDetails.vue'
import MediaGrid from '@/components/media/MediaGrid.vue'
import MediaThumb from '@/components/media/MediaThumb.vue'
import MediaUploader from '@/components/media/MediaUploader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'posts.edit_own' })
useHead({ title: 'Media · AFSC' })

const API = '/admin/api/v1'
const PAGE_SIZE = 40
const { authFetch, can } = useAuth()
const route = useRoute()

const view = ref<'grid' | 'list'>('grid')
try {
  view.value = localStorage.getItem('afsc:media-view') === 'list' ? 'list' : 'grid'
}
catch {}
watch(view, (v) => {
  try {
    localStorage.setItem('afsc:media-view', v)
  }
  catch {}
})

const q = ref('')
const type = ref('')
const legacy = ref(false)
const page = ref(1)
const data = ref<Page<MediaItem> | null>(null)
const loading = ref(false)
const error = ref('')
const showUpload = ref(false)
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<MediaItem>>(`${API}/media`, {
      query: { q: q.value, type: type.value || undefined, legacy: legacy.value ? 1 : undefined, page: page.value, page_size: PAGE_SIZE },
    })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load the library.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([q, type, legacy], () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 250)
})
watch(page, load)
load()

const detailsOpen = ref(false)
const detailsId = ref<number | null>(null)
function openDetails(id: number) {
  detailsId.value = id
  detailsOpen.value = true
}
const fromQuery = Number(route.query.id)
if (fromQuery > 0)
  openDetails(fromQuery)

function onUploaded(m: MediaItem, created: boolean) {
  if (!data.value)
    return
  if (!created)
    toast.info(`${m.filename} is already in the library.`)
  data.value.items = [m, ...data.value.items.filter(i => i.id !== m.id)]
}

function onChanged(m: MediaItem) {
  if (data.value)
    data.value.items = data.value.items.map(i => (i.id === m.id ? m : i))
}

function onDeleted(id: number) {
  if (data.value)
    data.value.items = data.value.items.filter(i => i.id !== id)
}

function size(n: number) {
  return n < 1 << 20 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / (1 << 20)).toFixed(1)} MB`
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Media" description="Every uploaded file. Old WordPress upload URLs keep working through their legacy paths.">
      <template #actions>
        <Button v-if="can('content.manage')" variant="outline" size="sm" as-child>
          <NuxtLink to="/media/legacy">
            <History />
            Legacy URLs
          </NuxtLink>
        </Button>
        <Button v-if="can('media.upload')" size="sm" @click="showUpload = !showUpload">
          <Upload />
          Upload
        </Button>
      </template>
    </PageHeader>

    <MediaUploader v-if="showUpload" @uploaded="onUploaded" />

    <div class="flex flex-wrap items-center gap-2">
      <Input v-model="q" placeholder="Search files" class="max-w-xs" />
      <NativeSelect v-model="type" aria-label="Type">
        <NativeSelectOption value="">
          All types
        </NativeSelectOption>
        <NativeSelectOption value="image">
          Images
        </NativeSelectOption>
        <NativeSelectOption value="video">
          Video
        </NativeSelectOption>
        <NativeSelectOption value="audio">
          Audio
        </NativeSelectOption>
        <NativeSelectOption value="pdf">
          PDF
        </NativeSelectOption>
      </NativeSelect>
      <div class="flex items-center gap-2 px-1">
        <Switch id="legacy-only" v-model="legacy" />
        <Label for="legacy-only" class="text-sm font-normal">Legacy files</Label>
      </div>
      <ToggleGroup :model-value="view" type="single" variant="outline" size="sm" class="ml-auto" aria-label="View" @update:model-value="v => v && (view = v as 'grid' | 'list')">
        <ToggleGroupItem value="grid" aria-label="Grid">
          <LayoutGrid />
        </ToggleGroupItem>
        <ToggleGroupItem value="list" aria-label="List">
          <List />
        </ToggleGroupItem>
      </ToggleGroup>
    </div>

    <FormError :message="error" />

    <div :class="{ 'opacity-60': loading }">
      <template v-if="view === 'grid'">
        <MediaGrid v-if="data?.items.length" :items="data.items" @pick="m => openDetails(m.id)" />
        <p v-else-if="data" class="rounded-md border py-12 text-center text-sm text-muted-foreground">
          No files match.
        </p>
      </template>
      <div v-else class="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow class="font-mono text-[10px] tracking-widest uppercase">
              <TableHead class="w-14">
                <span class="sr-only">Preview</span>
              </TableHead>
              <TableHead>File</TableHead>
              <TableHead class="hidden md:table-cell">
                Type
              </TableHead>
              <TableHead class="hidden md:table-cell">
                Size
              </TableHead>
              <TableHead class="hidden lg:table-cell">
                Uploaded
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="m in data?.items ?? []" :key="m.id" class="cursor-pointer" @click="openDetails(m.id)">
              <TableCell>
                <MediaThumb :item="m" class="size-10 rounded-sm" />
              </TableCell>
              <TableCell>
                <div class="max-w-[16rem] truncate font-medium sm:max-w-md">
                  {{ m.title || m.filename }}
                </div>
                <div class="flex flex-wrap items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
                  {{ m.filename }}
                  <Badge v-if="m.legacy_path_count" variant="secondary" class="rounded-sm text-[10px] uppercase">
                    {{ m.legacy_path_count }} legacy
                  </Badge>
                </div>
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
                {{ m.mime }}
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
                {{ size(m.bytes) }}
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground lg:table-cell">
                {{ fmt.format(new Date(m.created_at)) }}
              </TableCell>
            </TableRow>
            <TableEmpty v-if="data && !data.items.length" :colspan="5">
              No files match.
            </TableEmpty>
          </TableBody>
        </Table>
      </div>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <MediaDetails v-model:open="detailsOpen" :media-id="detailsId" @changed="onChanged" @deleted="onDeleted" />
  </div>
</template>
