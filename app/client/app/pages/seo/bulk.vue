<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { SeoBulkRow } from '@/types/api'
import { ExternalLink } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import SeoLengthMeter from '@/components/seo/SeoLengthMeter.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { API, errorData } from '@/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: 'Bulk SEO editor · AFSC' })

const PAGE_SIZE = 20
const { authFetch } = useAuth()

const page = ref(1)
const type = ref('')
const missing = ref('')
const q = ref('')
const data = ref<Page<SeoBulkRow> | null>(null)
const loading = ref(false)
const error = ref('')
const saving = ref(false)
// edits by post id; a key present means the field changed, '' clears the override
const edits = ref<Record<number, { title?: string, description?: string }>>({})
const changed = computed(() => Object.keys(edits.value).length)

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<SeoBulkRow>>(`${API}/seo/bulk`, {
      query: { page: page.value, page_size: PAGE_SIZE, type: type.value, missing: missing.value, q: q.value },
    })
    edits.value = {}
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load posts.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([type, missing, q], () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 250)
})
watch(page, load)
load()

function value(r: SeoBulkRow, key: 'title' | 'description') {
  return edits.value[r.post_id]?.[key] ?? r[key] ?? ''
}

function edit(r: SeoBulkRow, key: 'title' | 'description', v: string | number) {
  const next = { ...edits.value[r.post_id] }
  if (String(v) === (r[key] ?? ''))
    delete next[key]
  else
    next[key] = String(v)
  const all = { ...edits.value }
  if (Object.keys(next).length)
    all[r.post_id] = next
  else
    delete all[r.post_id]
  edits.value = all
}

async function save() {
  saving.value = true
  error.value = ''
  const rows = Object.entries(edits.value).map(([id, e]) => ({
    post_id: Number(id),
    ...('title' in e ? { title: e.title?.trim() || null } : {}),
    ...('description' in e ? { description: e.description?.trim() || null } : {}),
  }))
  try {
    const res = await authFetch<{ updated: number }>(`${API}/seo/bulk`, { method: 'PATCH', body: { rows } })
    toast.success(`Saved ${res.updated} ${res.updated === 1 ? 'post' : 'posts'}.`)
    await load()
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not save.'
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Bulk SEO editor" description="Fix SEO titles and meta descriptions in place. Empty fields fall back to the templates.">
      <template #actions>
        <Button v-if="changed" variant="outline" size="sm" :disabled="saving" @click="edits = {}">
          Discard
        </Button>
        <Button size="sm" :disabled="!changed || saving" @click="save">
          {{ saving ? 'Saving' : changed ? `Save ${changed} changed` : 'No changes' }}
        </Button>
      </template>
    </PageHeader>

    <div class="flex flex-wrap gap-2">
      <Input v-model="q" placeholder="Search titles" class="max-w-xs" :disabled="!!changed" />
      <NativeSelect v-model="type" aria-label="Type" :disabled="!!changed">
        <NativeSelectOption value="">
          Posts and pages
        </NativeSelectOption>
        <NativeSelectOption value="post">
          Posts
        </NativeSelectOption>
        <NativeSelectOption value="page">
          Pages
        </NativeSelectOption>
      </NativeSelect>
      <NativeSelect v-model="missing" aria-label="Missing" :disabled="!!changed">
        <NativeSelectOption value="">
          All
        </NativeSelectOption>
        <NativeSelectOption value="title">
          Missing SEO title
        </NativeSelectOption>
        <NativeSelectOption value="description">
          Missing description
        </NativeSelectOption>
      </NativeSelect>
    </div>

    <FormError :message="error" />

    <div class="grid gap-3" :class="{ 'opacity-60': loading }">
      <div class="hidden grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)_minmax(0,1.7fr)] gap-4 px-3 font-mono text-[10px] tracking-widest text-muted-foreground uppercase md:grid">
        <span>Post</span>
        <span>SEO title</span>
        <span>Meta description</span>
      </div>
      <div
        v-for="r in data?.items ?? []"
        :key="r.post_id"
        class="grid gap-3 rounded-md border p-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)_minmax(0,1.7fr)] md:gap-4"
        :class="{ 'border-primary/50': edits[r.post_id] }"
      >
        <div class="grid content-start gap-1">
          <div class="flex items-start gap-1.5">
            <span class="min-w-0 text-sm font-medium break-words">{{ r.post_title || '(no title)' }}</span>
            <a v-if="r.url" :href="r.url" target="_blank" rel="noopener" class="mt-0.5 shrink-0 text-muted-foreground hover:text-foreground" :aria-label="`Open ${r.post_title}`">
              <ExternalLink class="size-3.5" />
            </a>
          </div>
          <div class="flex flex-wrap gap-1.5">
            <Badge variant="outline" class="rounded-sm text-[10px] uppercase">
              {{ r.type }}
            </Badge>
            <Badge v-if="r.status !== 'published'" variant="secondary" class="rounded-sm text-[10px] uppercase">
              {{ r.status }}
            </Badge>
          </div>
        </div>
        <div class="grid content-start gap-1.5">
          <Input
            :model-value="value(r, 'title')"
            :placeholder="r.resolved_title"
            :aria-label="`SEO title of ${r.post_title}`"
            @update:model-value="edit(r, 'title', $event)"
          />
          <SeoLengthMeter :text="edits[r.post_id]?.title !== undefined && value(r, 'title') ? value(r, 'title') : r.resolved_title" kind="title" />
        </div>
        <div class="grid content-start gap-1.5">
          <Textarea
            :model-value="value(r, 'description')"
            :placeholder="r.resolved_description || 'No description'"
            rows="2"
            :aria-label="`Meta description of ${r.post_title}`"
            @update:model-value="edit(r, 'description', $event)"
          />
          <SeoLengthMeter :text="edits[r.post_id]?.description !== undefined && value(r, 'description') ? value(r, 'description') : r.resolved_description" kind="description" />
        </div>
      </div>
      <p v-if="data && !data.items.length" class="rounded-md border p-6 text-center text-sm text-muted-foreground">
        Nothing matches.
      </p>
    </div>
    <p v-if="changed" class="text-xs text-muted-foreground">
      Save or discard your changes to filter or change pages.
    </p>
    <PagePagination v-else-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />
  </div>
</template>
