<script setup lang="ts">
import type { VideoRow } from '@/types/api'
import { Pencil, Plus, Trash2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { API, errorData } from '@/composables/useAuth'

const props = defineProps<{ postId: number }>()
const { authFetch } = useAuth()

interface Draft extends Omit<VideoRow, 'upload_date'> { upload_day: string }

const rows = ref<VideoRow[]>([])
const manual = ref<Draft[]>([])
const saved = ref('')
const saving = ref(false)
const error = ref('')

function toDraft(v: VideoRow): Draft {
  const { upload_date, ...rest } = v
  return { ...rest, source: 'manual', upload_day: upload_date ? upload_date.slice(0, 10) : '' }
}

async function load() {
  error.value = ''
  try {
    rows.value = (await authFetch<{ items: VideoRow[] }>(`${API}/seo/videos/${props.postId}`)).items
    manual.value = rows.value.filter(v => v.source === 'manual').map(toDraft)
    saved.value = JSON.stringify(manual.value)
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load videos.'
  }
}
watch(() => props.postId, load, { immediate: true })

const overridden = computed(() => new Set(manual.value.map(m => m.url.trim())))
const detected = computed(() => rows.value.filter(v => v.source !== 'manual'))
const dirty = computed(() => JSON.stringify(manual.value) !== saved.value)

function add(from?: VideoRow) {
  manual.value.push(from
    ? { ...toDraft(from), id: null }
    : { id: null, url: '', provider: '', title: '', description: '', thumbnail_url: '', duration_seconds: null, upload_day: '', family_friendly: null, source: 'manual' })
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    const items: VideoRow[] = manual.value.map(({ upload_day, ...v }) => ({
      ...v,
      duration_seconds: v.duration_seconds === null || String(v.duration_seconds) === '' ? null : Number(v.duration_seconds),
      upload_date: upload_day ? new Date(`${upload_day}T00:00:00Z`).toISOString() : null,
    }))
    rows.value = (await authFetch<{ items: VideoRow[] }>(`${API}/seo/videos/${props.postId}`, { method: 'PUT', body: { items } })).items
    manual.value = rows.value.filter(v => v.source === 'manual').map(toDraft)
    saved.value = JSON.stringify(manual.value)
    toast.success('Videos saved.')
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not save the videos.'
  }
  finally {
    saving.value = false
  }
}

const labelClass = 'font-mono text-[10px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <div class="grid gap-3">
    <p class="text-xs text-muted-foreground">
      Videos found in the content are listed automatically. Override one to change what search engines see; your row wins for the same URL.
    </p>
    <ul v-if="detected.length" class="grid gap-1.5">
      <li v-for="v in detected" :key="v.id ?? v.url" class="flex items-center gap-2 rounded-md border px-2 py-1.5 text-sm">
        <Badge variant="secondary" class="rounded-sm font-mono text-[10px] uppercase">
          {{ v.source }}
        </Badge>
        <span class="min-w-0 flex-1 truncate" :title="v.url">{{ v.title || v.url }}</span>
        <Badge v-if="overridden.has(v.url)" variant="outline" class="rounded-sm text-[10px]">
          overridden
        </Badge>
        <Button v-else type="button" variant="ghost" size="icon-xs" :aria-label="`Override ${v.url}`" @click="add(v)">
          <Pencil />
        </Button>
      </li>
    </ul>

    <div v-for="(m, i) in manual" :key="i" class="grid gap-2 rounded-md border p-3">
      <div class="flex items-center justify-between">
        <span :class="labelClass">Video {{ i + 1 }}</span>
        <Button type="button" variant="ghost" size="icon-xs" aria-label="Remove video" @click="manual.splice(i, 1)">
          <Trash2 />
        </Button>
      </div>
      <div class="grid gap-1">
        <Label :class="labelClass" :for="`v-url-${i}`">URL</Label>
        <Input :id="`v-url-${i}`" v-model="m.url" placeholder="https://" />
      </div>
      <div class="grid gap-1">
        <Label :class="labelClass" :for="`v-title-${i}`">Title</Label>
        <Input :id="`v-title-${i}`" v-model="m.title" />
      </div>
      <div class="grid gap-1">
        <Label :class="labelClass" :for="`v-desc-${i}`">Description</Label>
        <Textarea :id="`v-desc-${i}`" v-model="m.description" rows="2" />
      </div>
      <div class="grid gap-1">
        <Label :class="labelClass" :for="`v-thumb-${i}`">Thumbnail URL</Label>
        <Input :id="`v-thumb-${i}`" v-model="m.thumbnail_url" placeholder="https://" />
      </div>
      <div class="grid grid-cols-2 gap-2">
        <div class="grid gap-1">
          <Label :class="labelClass" :for="`v-dur-${i}`">Seconds</Label>
          <Input :id="`v-dur-${i}`" v-model="m.duration_seconds" type="number" min="0" inputmode="numeric" />
        </div>
        <div class="grid gap-1">
          <Label :class="labelClass" :for="`v-date-${i}`">Upload date</Label>
          <Input :id="`v-date-${i}`" v-model="m.upload_day" type="date" />
        </div>
      </div>
    </div>

    <FormError :message="error" />
    <div class="flex flex-wrap gap-2">
      <Button type="button" variant="outline" size="sm" @click="add()">
        <Plus />
        Add video
      </Button>
      <Button type="button" size="sm" :disabled="!dirty || saving" @click="save">
        {{ saving ? 'Saving' : 'Save videos' }}
      </Button>
    </div>
  </div>
</template>
