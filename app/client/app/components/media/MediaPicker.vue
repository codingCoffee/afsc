<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { MediaItem } from '@/types/api'
import { ref, watch } from 'vue'
import MediaGrid from '@/components/media/MediaGrid.vue'
import MediaUploader from '@/components/media/MediaUploader.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { API } from '~/composables/useAuth'

const props = withDefaults(defineProps<{ accept?: 'image' | 'video' | 'any', multiple?: boolean }>(), { accept: 'image', multiple: false })
const emit = defineEmits<{ select: [items: MediaItem[]] }>()
const open = defineModel<boolean>('open', { default: false })

const PAGE_SIZE = 30
const { authFetch, can } = useAuth()
const tab = ref('library')
const q = ref('')
const items = ref<MediaItem[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const error = ref('')
const selected = ref<MediaItem[]>([])

async function load(reset: boolean) {
  if (reset)
    page.value = 1
  loading.value = true
  error.value = ''
  try {
    const res = await authFetch<Page<MediaItem>>(`${API}/media`, {
      query: { q: q.value, type: props.accept === 'any' ? undefined : props.accept, page: page.value, page_size: PAGE_SIZE },
    })
    items.value = reset ? res.items : [...items.value, ...res.items]
    total.value = res.total
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load the library.'
  }
  finally {
    loading.value = false
  }
}

watch(open, (v) => {
  if (v) {
    selected.value = []
    tab.value = 'library'
    load(true)
  }
})

let timer: ReturnType<typeof setTimeout> | undefined
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => load(true), 250)
})

function more() {
  page.value++
  load(false)
}

function toggle(m: MediaItem) {
  const on = selected.value.some(s => s.id === m.id)
  if (!props.multiple)
    selected.value = on ? [] : [m]
  else
    selected.value = on ? selected.value.filter(s => s.id !== m.id) : [...selected.value, m]
}

function onUploaded(m: MediaItem) {
  items.value = [m, ...items.value.filter(i => i.id !== m.id)]
  if (!selected.value.some(s => s.id === m.id))
    selected.value = props.multiple ? [...selected.value, m] : [m]
  tab.value = 'library'
}

function confirm() {
  emit('select', selected.value)
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="flex max-h-[90dvh] flex-col sm:max-w-4xl">
      <DialogHeader>
        <DialogTitle>{{ multiple ? 'Choose files' : 'Choose a file' }}</DialogTitle>
        <DialogDescription>Pick from the library or upload new files.</DialogDescription>
      </DialogHeader>
      <Tabs v-model="tab" class="min-h-0 flex-1">
        <TabsList>
          <TabsTrigger value="library">
            Library
          </TabsTrigger>
          <TabsTrigger v-if="can('media.upload')" value="upload">
            Upload
          </TabsTrigger>
        </TabsList>
        <TabsContent value="library" class="grid min-h-0 gap-3 overflow-y-auto">
          <Input v-model="q" placeholder="Search files" class="max-w-xs" />
          <FormError :message="error" />
          <MediaGrid :items="items" :selected="selected.map(s => s.id)" @pick="toggle" />
          <p v-if="!loading && !items.length" class="py-8 text-center text-sm text-muted-foreground">
            Nothing here yet.
          </p>
          <Button v-if="items.length < total" variant="outline" size="sm" class="justify-self-center" :disabled="loading" @click="more">
            Load more
          </Button>
        </TabsContent>
        <TabsContent value="upload">
          <MediaUploader :accept="accept" @uploaded="onUploaded" />
        </TabsContent>
      </Tabs>
      <DialogFooter>
        <Button variant="outline" @click="open = false">
          Cancel
        </Button>
        <Button :disabled="!selected.length" @click="confirm">
          {{ multiple && selected.length > 1 ? `Select ${selected.length}` : 'Select' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
