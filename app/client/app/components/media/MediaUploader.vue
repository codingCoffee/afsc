<script lang="ts">
import type { MediaItem } from '@/types/api'
import { API } from '~/composables/useAuth'

export interface UploadResult { status: number, data: Record<string, unknown> }

// XHR rather than fetch, for upload progress; the body streams as-is, one file per request
export function sendFile(url: string, method: 'POST' | 'PUT', file: File, token: string | null, onProgress?: (pct: number) => void): Promise<UploadResult> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(method, url)
    xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream')
    xhr.setRequestHeader('X-Filename', encodeURIComponent(file.name))
    if (token)
      xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable)
        onProgress?.(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      let data: Record<string, unknown> = {}
      try {
        data = JSON.parse(xhr.responseText || '{}')
      }
      catch {}
      resolve({ status: xhr.status, data })
    }
    xhr.onerror = () => reject(new Error('network error'))
    xhr.send(file)
  })
}

// sendFile with one retry after refreshing an expired access token
export async function uploadWithAuth(url: string, method: 'POST' | 'PUT', file: File, onProgress?: (pct: number) => void): Promise<UploadResult> {
  const { accessToken, refresh } = useAuth()
  let res = await sendFile(url, method, file, accessToken.value, onProgress)
  if (res.status === 401 && await refresh())
    res = await sendFile(url, method, file, accessToken.value, onProgress)
  return res
}
</script>

<script setup lang="ts">
import { CloudUpload, X } from '@lucide/vue'
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'

const props = withDefaults(defineProps<{ accept?: 'image' | 'video' | 'any', compact?: boolean }>(), { accept: 'any' })
const emit = defineEmits<{ uploaded: [item: MediaItem, created: boolean] }>()

interface Job { id: number, name: string, pct: number, error: string, done: boolean }
const jobs = ref<Job[]>([])
const dragging = ref(false)
const input = ref<HTMLInputElement | null>(null)
let seq = 0

const acceptAttr: Record<string, string> = {
  image: 'image/jpeg,image/png,image/gif,image/webp,image/avif,image/svg+xml',
  video: 'video/mp4,video/webm',
  any: 'image/jpeg,image/png,image/gif,image/webp,image/avif,image/svg+xml,video/mp4,video/webm,audio/mpeg,application/pdf',
}

async function upload(file: File) {
  const job: Job = { id: ++seq, name: file.name, pct: 0, error: '', done: false }
  jobs.value.unshift(job)
  const live = () => jobs.value.find(j => j.id === job.id)!
  try {
    const res = await uploadWithAuth(`${API}/media`, 'POST', file, (pct) => {
      live().pct = pct
    })
    if (res.status === 200 || res.status === 201) {
      live().done = true
      live().pct = 100
      emit('uploaded', res.data as unknown as MediaItem, res.status === 201)
      setTimeout(() => {
        jobs.value = jobs.value.filter(j => j.id !== job.id)
      }, 1500)
    }
    else {
      live().error = (res.data.message as string) ?? `Upload failed (${res.status}).`
    }
  }
  catch {
    live().error = 'Network error. Try again.'
  }
}

function addFiles(list: FileList | null | undefined) {
  for (const f of Array.from(list ?? []))
    upload(f)
}

function onDrop(e: DragEvent) {
  dragging.value = false
  addFiles(e.dataTransfer?.files)
}

function onPick(e: Event) {
  const el = e.target as HTMLInputElement
  addFiles(el.files)
  el.value = ''
}

function dismiss(id: number) {
  jobs.value = jobs.value.filter(j => j.id !== id)
}

defineExpose({ pick: () => input.value?.click() })
</script>

<template>
  <div class="grid gap-3">
    <label
      class="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-md border border-dashed text-center text-sm text-muted-foreground transition-colors hover:bg-muted/40"
      :class="[dragging ? 'border-primary bg-muted/60' : '', props.compact ? 'p-4' : 'p-8']"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
    >
      <CloudUpload class="size-6" />
      <span>Drop files here or <span class="text-foreground underline underline-offset-4">browse</span></span>
      <span class="font-mono text-[11px]">One file per upload; several at once is fine.</span>
      <input ref="input" type="file" multiple class="sr-only" :accept="acceptAttr[props.accept]" @change="onPick">
    </label>
    <ul v-if="jobs.length" class="grid gap-2">
      <li v-for="j in jobs" :key="j.id" class="grid gap-1.5 rounded-md border p-2.5">
        <div class="flex items-center gap-2 text-sm">
          <span class="min-w-0 flex-1 truncate" :title="j.name">{{ j.name }}</span>
          <span class="font-mono text-[11px] text-muted-foreground">{{ j.error ? 'failed' : j.done ? 'done' : `${j.pct}%` }}</span>
          <Button v-if="j.error" variant="ghost" size="icon-xs" aria-label="Dismiss" @click="dismiss(j.id)">
            <X />
          </Button>
        </div>
        <Progress v-if="!j.error" :model-value="j.pct" class="h-1" />
        <p v-else role="alert" class="font-mono text-xs text-destructive">
          {{ j.error }}
        </p>
      </li>
    </ul>
  </div>
</template>
