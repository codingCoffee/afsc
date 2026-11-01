<script setup lang="ts">
import type { MediaDetail, MediaItem } from '@/types/api'
import { Copy, Crosshair, Replace, Trash2 } from '@lucide/vue'
import { computed, reactive, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import LegacyPathsConfirm from '@/components/media/LegacyPathsConfirm.vue'
import MediaThumb from '@/components/media/MediaThumb.vue'
import { uploadWithAuth } from '@/components/media/MediaUploader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'

const props = defineProps<{ mediaId: number | null }>()
const emit = defineEmits<{ changed: [item: MediaItem], deleted: [id: number] }>()
const open = defineModel<boolean>('open', { default: false })

const API = '/admin/api/v1'
const { authFetch, can, user } = useAuth()
const detail = ref<MediaDetail | null>(null)
const form = reactive({ title: '', alt: '', caption: '', description: '', focal_x: null as number | null, focal_y: null as number | null })
const error = ref('')
const saving = ref(false)
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })

const editable = computed(() => can('media.upload') && (can('content.manage') || detail.value?.uploaded_by === user.value?.id))
const isImage = computed(() => !!detail.value?.thumb_url && detail.value.mime !== 'image/svg+xml')

async function load() {
  if (!props.mediaId)
    return
  error.value = ''
  try {
    detail.value = await authFetch<MediaDetail>(`${API}/media/${props.mediaId}`)
    const d = detail.value
    Object.assign(form, { title: d.title, alt: d.alt, caption: d.caption, description: d.description, focal_x: d.focal_x, focal_y: d.focal_y })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load this file.'
  }
}
watch(() => [open.value, props.mediaId], () => {
  if (open.value)
    load()
}, { immediate: true })

function pickFocal(e: MouseEvent) {
  if (!editable.value)
    return
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  form.focal_x = Math.min(1, Math.max(0, Math.round(((e.clientX - r.left) / r.width) * 1000) / 1000))
  form.focal_y = Math.min(1, Math.max(0, Math.round(((e.clientY - r.top) / r.height) * 1000) / 1000))
}

async function save() {
  if (!detail.value)
    return
  saving.value = true
  error.value = ''
  const body: Record<string, unknown> = { title: form.title, alt: form.alt, caption: form.caption, description: form.description }
  if (form.focal_x != null && form.focal_y != null)
    Object.assign(body, { focal_x: form.focal_x, focal_y: form.focal_y })
  try {
    const item = await authFetch<MediaItem>(`${API}/media/${detail.value.id}`, { method: 'PATCH', body })
    detail.value = { ...detail.value, ...item }
    emit('changed', item)
    toast.success('Saved.')
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not save.'
  }
  finally {
    saving.value = false
  }
}

async function copyURL() {
  if (!detail.value)
    return
  await navigator.clipboard.writeText(location.origin + detail.value.url)
  toast.success('URL copied.')
}

// replace: pick a file, confirm when legacy URLs serve the current one, then upload with progress
const fileInput = ref<HTMLInputElement | null>(null)
const pending = ref<File | null>(null)
const confirmReplace = ref(false)
const progress = ref<number | null>(null)

function onReplacePick(e: Event) {
  const el = e.target as HTMLInputElement
  const f = el.files?.[0]
  el.value = ''
  if (!f || !detail.value)
    return
  pending.value = f
  if (detail.value.legacy_paths.length)
    confirmReplace.value = true
  else
    replace(false)
}

async function replace(confirmed: boolean) {
  const f = pending.value
  if (!f || !detail.value)
    return
  progress.value = 0
  try {
    const res = await uploadWithAuth(`${API}/media/${detail.value.id}/file${confirmed ? '?confirm=1' : ''}`, 'PUT', f, (p) => {
      progress.value = p
    })
    if (res.status === 409 && res.data.code === 'legacy_paths') {
      detail.value.legacy_paths = res.data.legacy_paths as MediaDetail['legacy_paths']
      confirmReplace.value = true
      return
    }
    if (res.status !== 200) {
      toast.error((res.data.message as string) ?? 'Could not replace the file.')
      return
    }
    pending.value = null
    emit('changed', res.data as unknown as MediaItem)
    toast.success('File replaced. Old URLs now redirect.')
    await load()
  }
  catch {
    toast.error('Network error. Try again.')
  }
  finally {
    progress.value = null
  }
}

const confirmDelete = ref(false)
async function remove() {
  if (!detail.value)
    return
  const id = detail.value.id
  try {
    await authFetch(`${API}/media/${id}`, { method: 'DELETE', query: detail.value.legacy_paths.length ? { confirm: 1 } : undefined })
    emit('deleted', id)
    open.value = false
    toast.success('Deleted.')
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not delete.')
  }
}

function size(n: number) {
  return n < 1 << 20 ? `${Math.max(1, Math.round(n / 1024))} KB` : `${(n / (1 << 20)).toFixed(1)} MB`
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full overflow-y-auto sm:max-w-lg">
      <SheetHeader>
        <SheetTitle class="truncate pr-6">
          {{ detail?.filename ?? 'File' }}
        </SheetTitle>
        <SheetDescription v-if="detail" class="font-mono text-[11px]">
          {{ detail.mime }} · {{ size(detail.bytes) }}<template v-if="detail.width">
            · {{ detail.width }}×{{ detail.height }}
          </template> · {{ fmt.format(new Date(detail.created_at)) }}
        </SheetDescription>
      </SheetHeader>

      <div v-if="detail" class="grid gap-5 px-4 pb-6">
        <div v-if="isImage" class="grid gap-1.5">
          <div class="relative mx-auto w-fit" :class="editable ? 'cursor-crosshair' : ''" @click="pickFocal">
            <img :src="detail.thumb_url!" :alt="detail.alt" class="block max-h-72 rounded-md border">
            <span
              v-if="form.focal_x != null && form.focal_y != null"
              class="pointer-events-none absolute size-4 -translate-1/2 rounded-full border-2 border-white bg-primary shadow"
              :style="{ left: `${form.focal_x * 100}%`, top: `${form.focal_y * 100}%` }"
            />
          </div>
          <p v-if="editable" class="flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
            <Crosshair class="size-3" /> Click the image to set the focal point used for crops.
          </p>
        </div>
        <MediaThumb v-else :item="detail" class="h-32 rounded-md border" />

        <div class="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" @click="copyURL">
            <Copy /> Copy URL
          </Button>
          <Button v-if="editable" variant="outline" size="sm" :disabled="progress !== null" @click="fileInput?.click()">
            <Replace /> Replace file
          </Button>
          <Button v-if="can('content.manage')" variant="outline" size="sm" class="text-destructive" @click="confirmDelete = true">
            <Trash2 /> Delete
          </Button>
          <input ref="fileInput" type="file" class="sr-only" @change="onReplacePick">
        </div>
        <Progress v-if="progress !== null" :model-value="progress" class="h-1" />

        <form class="grid gap-4" @submit.prevent="save">
          <div class="grid gap-2">
            <Label for="md-title">Title</Label>
            <Input id="md-title" v-model="form.title" :disabled="!editable" />
          </div>
          <div v-if="detail.mime.startsWith('image/')" class="grid gap-2">
            <Label for="md-alt">Alt text</Label>
            <Input id="md-alt" v-model="form.alt" :disabled="!editable" placeholder="Describe the image for screen readers" />
          </div>
          <div class="grid gap-2">
            <Label for="md-caption">Caption</Label>
            <Textarea id="md-caption" v-model="form.caption" rows="2" :disabled="!editable" />
          </div>
          <div class="grid gap-2">
            <Label for="md-desc">Description</Label>
            <Textarea id="md-desc" v-model="form.description" rows="3" :disabled="!editable" />
          </div>
          <FormError :message="error" />
          <Button v-if="editable" type="submit" size="sm" class="justify-self-start" :disabled="saving">
            {{ saving ? 'Saving' : 'Save' }}
          </Button>
        </form>

        <section class="grid gap-2">
          <h3 class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
            Used in
          </h3>
          <p v-if="!detail.used_in.length" class="text-sm text-muted-foreground">
            Not used in any post or page.
          </p>
          <ul v-else class="divide-y rounded-md border">
            <li v-for="p in detail.used_in" :key="p.id" class="flex items-center gap-2 px-3 py-2 text-sm">
              <span class="min-w-0 flex-1 truncate">{{ p.title || '(untitled)' }}</span>
              <Badge variant="outline" class="rounded-sm text-[10px] uppercase">
                {{ p.type }} · {{ p.status }}
              </Badge>
            </li>
          </ul>
        </section>

        <section v-if="detail.legacy_paths.length" class="grid gap-2">
          <h3 class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
            Legacy URLs
          </h3>
          <ul class="divide-y rounded-md border">
            <li v-for="p in detail.legacy_paths" :key="p.path" class="flex items-center gap-2 px-3 py-2">
              <span class="min-w-0 flex-1 truncate font-mono text-xs" :title="p.path">{{ p.path }}</span>
              <span class="shrink-0 font-mono text-[11px] text-muted-foreground">{{ p.hits }} hits</span>
            </li>
          </ul>
        </section>
      </div>
      <FormError v-else-if="error" :message="error" class="px-4" />

      <LegacyPathsConfirm
        v-model:open="confirmReplace"
        title="Replace this file?"
        description="These old WordPress URLs will serve the new file. Posts keep pointing at this item."
        action="Replace"
        :paths="detail?.legacy_paths ?? []"
        @confirm="replace(true)"
      />
      <LegacyPathsConfirm
        v-model:open="confirmDelete"
        :title="`Delete ${detail?.filename ?? ''}?`"
        :description="detail?.legacy_paths.length ? 'These old WordPress URLs will stop working. Posts that use the file lose it. This can\'t be undone.' : 'Posts that use the file lose it. This can\'t be undone.'"
        action="Delete"
        :paths="detail?.legacy_paths ?? []"
        @confirm="remove"
      />
    </SheetContent>
  </Sheet>
</template>
