<script setup lang="ts">
import type { DiffOp, PMNode, RevisionDiff, RevisionItem, SeoFields } from '~/types/api'
import { History } from '@lucide/vue'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { API } from '~/composables/useAuth'

export interface RestoredRevision { title?: string, excerpt?: string, content: PMNode, seo?: SeoFields | null }

const props = defineProps<{ postId: number }>()
const emit = defineEmits<{ restore: [rev: RestoredRevision] }>()
const open = defineModel<boolean>('open', { default: false })
const { authFetch } = useAuth()

const items = ref<RevisionItem[]>([])
const selected = ref<RevisionItem | null>(null)
const diff = ref<RevisionDiff | null>(null)
const loading = ref(false)
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

watch(open, async (v) => {
  if (!v)
    return
  selected.value = null
  diff.value = null
  try {
    const res = await authFetch<RevisionItem[] | { items: RevisionItem[] }>(`${API}/posts/${props.postId}/revisions`)
    items.value = Array.isArray(res) ? res : res.items
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not load revisions.')
  }
})

async function select(r: RevisionItem) {
  selected.value = r
  diff.value = null
  loading.value = true
  try {
    diff.value = await authFetch<RevisionDiff>(`${API}/revisions/${r.id}/diff`, { query: { against: 'current' } })
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not load the diff.')
  }
  finally {
    loading.value = false
  }
}

async function restore() {
  if (!selected.value)
    return
  try {
    const res = await authFetch<RestoredRevision | PMNode>(`${API}/revisions/${selected.value.id}/restore`, { method: 'POST' })
    emit('restore', 'content' in res && res.content && typeof res.content === 'object' && !Array.isArray(res.content) ? res as RestoredRevision : { content: res as PMNode })
    open.value = false
    toast.success('Revision loaded into the editor. Save to keep it.')
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not restore that revision.')
  }
}

// left side is the selected revision, right side the current post
const side = (ops: DiffOp[], keep: 'delete' | 'insert') => ops.filter(o => o.op === 'equal' || o.op === keep)
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full overflow-y-auto sm:max-w-3xl">
      <SheetHeader>
        <SheetTitle>Revisions</SheetTitle>
        <SheetDescription>Compare a revision with the current post. Restoring loads it into the editor without saving.</SheetDescription>
      </SheetHeader>
      <div class="grid gap-4 px-4 pb-6">
        <ul class="grid max-h-60 gap-1 overflow-y-auto rounded-md border p-1">
          <li v-for="r in items" :key="r.id">
            <button
              type="button"
              class="flex w-full flex-wrap items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-accent"
              :class="{ 'bg-accent': selected?.id === r.id }"
              @click="select(r)"
            >
              <History class="size-3.5 text-muted-foreground" />
              <span class="font-mono text-xs">{{ fmt.format(new Date(r.created_at)) }}</span>
              <Badge variant="outline" class="rounded-sm text-[10px] uppercase">
                {{ r.kind }}
              </Badge>
              <span class="truncate text-muted-foreground">{{ r.user_name ?? 'system' }}</span>
            </button>
          </li>
          <li v-if="!items.length" class="px-2 py-1.5 text-sm text-muted-foreground">
            No revisions yet.
          </li>
        </ul>

        <p v-if="loading" class="text-sm text-muted-foreground">
          Loading diff…
        </p>
        <template v-if="diff && selected">
          <div class="grid gap-3 md:grid-cols-2">
            <div v-for="(keep, i) in (['delete', 'insert'] as const)" :key="keep" class="min-w-0 rounded-md border">
              <div class="border-b px-3 py-2 font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
                {{ i === 0 ? 'This revision' : 'Current' }}
              </div>
              <div class="grid gap-2 p-3 text-sm">
                <p class="font-semibold">
                  <span v-for="(o, j) in side(diff.title, keep)" :key="j" :class="o.op !== 'equal' && (keep === 'delete' ? 'bg-destructive/15 line-through' : 'bg-emerald-500/20')">{{ o.text }}</span>
                </p>
                <p class="whitespace-pre-wrap break-words">
                  <span v-for="(o, j) in side(diff.text, keep)" :key="j" :class="o.op !== 'equal' && (keep === 'delete' ? 'bg-destructive/15 line-through' : 'bg-emerald-500/20')">{{ o.text }}</span>
                </p>
              </div>
            </div>
          </div>
          <ul v-if="diff.blocks.length" class="grid gap-1 text-xs text-muted-foreground">
            <li v-for="b in diff.blocks" :key="`${b.index}-${b.change}`">
              Block {{ b.index + 1 }} ({{ b.type }}) {{ b.change }}
            </li>
          </ul>
          <Button class="justify-self-start" @click="restore">
            Restore this revision
          </Button>
        </template>
      </div>
    </SheetContent>
  </Sheet>
</template>
