<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { AdminComment, CommentAction, CommentChange, CommentList, CommentStatus } from '@/types/api'
import { Check, ExternalLink, MessageSquareReply, RotateCcw, ShieldAlert, ShieldCheck, Trash2, Undo2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { API, errorData } from '@/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'comments.moderate' })
useHead({ title: 'Comments · AFSC' })

const PAGE_SIZE = 30
const TABS: { value: CommentStatus, label: string }[] = [
  { value: 'pending', label: 'Pending' },
  { value: 'approved', label: 'Approved' },
  { value: 'spam', label: 'Spam' },
  { value: 'trash', label: 'Trash' },
]

const route = useRoute()
const { authFetch } = useAuth()

const status = ref<CommentStatus>(TABS.some(t => t.value === route.query.status) ? route.query.status as CommentStatus : 'pending')
const page = ref(1)
const q = ref('')
const data = ref<CommentList | null>(null)
const loading = ref(false)
const error = ref('')
const selected = ref<Set<number>>(new Set())
const postID = computed(() => Number(route.query.post_id) || undefined)

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<CommentList>(`${API}/comments`, {
      query: { status: status.value, page: page.value, page_size: PAGE_SIZE, q: q.value, post_id: postID.value },
    })
    selected.value = new Set()
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load comments.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 250)
})
watch(status, () => {
  page.value = 1
  load()
})
watch([page, postID], load)
load()

const ids = computed(() => data.value?.items.map(c => c.id) ?? [])
const allSelected = computed(() => ids.value.length > 0 && ids.value.every(id => selected.value.has(id)))

function toggle(id: number, on: boolean) {
  const next = new Set(selected.value)
  if (on)
    next.add(id)
  else
    next.delete(id)
  selected.value = next
}

const ACTION_LABELS: Record<CommentAction, string> = {
  approve: 'Approve',
  unapprove: 'Unapprove',
  spam: 'Spam',
  not_spam: 'Not spam',
  trash: 'Trash',
  restore: 'Restore',
  delete: 'Delete permanently',
}
const ACTION_ICONS = { approve: Check, unapprove: Undo2, spam: ShieldAlert, not_spam: ShieldCheck, trash: Trash2, restore: RotateCcw, delete: Trash2 }

// the moderation actions offered in each tab
const tabActions: Record<CommentStatus, CommentAction[]> = {
  pending: ['approve', 'spam', 'trash'],
  approved: ['unapprove', 'spam', 'trash'],
  spam: ['not_spam', 'delete'],
  trash: ['restore', 'delete'],
}

const confirmOpen = ref(false)
const pendingDelete = ref<number[]>([])

function run(action: CommentAction, list: number[]) {
  if (action === 'delete') {
    pendingDelete.value = list
    confirmOpen.value = true
    return
  }
  apply(action, list)
}

async function apply(action: CommentAction, list: number[]) {
  try {
    const res = await authFetch<{ changed: CommentChange[] }>(`${API}/comments/bulk`, { method: 'POST', body: { ids: list, action } })
    const n = res.changed.length
    toast.success(`${ACTION_LABELS[action]}: ${n} comment${n === 1 ? '' : 's'}`)
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not update comments.')
  }
}

const replyTo = ref<AdminComment | null>(null)
const replyBody = ref('')
const replying = ref(false)
const replyOpen = computed({
  get: () => replyTo.value !== null,
  set: (v) => {
    if (!v)
      replyTo.value = null
  },
})

function openReply(c: AdminComment) {
  replyTo.value = c
  replyBody.value = ''
}

async function sendReply() {
  if (!replyTo.value)
    return
  replying.value = true
  try {
    await authFetch(`${API}/comments/${replyTo.value.id}/reply`, { method: 'POST', body: { body: replyBody.value } })
    toast.success('Reply published')
    replyTo.value = null
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not post the reply.')
  }
  finally {
    replying.value = false
  }
}

function actions(c: AdminComment): Action[] {
  const list: Action[] = tabActions[c.status].map(a => ({
    label: ACTION_LABELS[a],
    icon: ACTION_ICONS[a],
    danger: a === 'delete',
    run: () => run(a, [c.id]),
  }))
  if (c.status === 'approved' || c.status === 'pending')
    list.unshift({ label: 'Reply', icon: MessageSquareReply, run: () => openReply(c) })
  if (c.post_url)
    list.push({ label: 'View post', icon: ExternalLink, run: () => window.open(c.post_url, '_blank', 'noopener') })
  return list
}

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Comments" description="Approve, reply to or mark comments as spam. Spam and Not spam also train the spam filter.">
      <template #actions>
        <Button
          v-for="a in tabActions[status]"
          :key="a"
          :variant="a === 'delete' ? 'destructive' : 'outline'"
          size="sm"
          :disabled="!selected.size"
          @click="run(a, [...selected])"
        >
          <component :is="ACTION_ICONS[a]" />
          {{ ACTION_LABELS[a] }} {{ selected.size || '' }}
        </Button>
      </template>
    </PageHeader>

    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <Tabs v-model="status">
        <TabsList class="flex-wrap">
          <TabsTrigger v-for="t in TABS" :key="t.value" :value="t.value">
            {{ t.label }}
            <span class="ml-1 font-mono text-[11px] text-muted-foreground">{{ data?.counts[t.value] ?? '' }}</span>
          </TabsTrigger>
        </TabsList>
      </Tabs>
      <Input v-model="q" placeholder="Search text, name, email or IP" class="sm:max-w-xs" />
    </div>

    <p v-if="postID" class="text-sm text-muted-foreground">
      Showing comments on one post.
      <NuxtLink :to="{ query: { status } }" class="underline">
        Show all
      </NuxtLink>
    </p>

    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead class="w-10">
              <Checkbox :model-value="allSelected" aria-label="Select all" @update:model-value="selected = $event === true ? new Set(ids) : new Set()" />
            </TableHead>
            <TableHead>Comment</TableHead>
            <TableHead class="hidden lg:table-cell">
              In response to
            </TableHead>
            <TableHead class="hidden md:table-cell">
              Spam score
            </TableHead>
            <TableHead class="w-12">
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="c in data?.items ?? []" :key="c.id" class="align-top">
            <TableCell>
              <Checkbox :model-value="selected.has(c.id)" :aria-label="`Select comment by ${c.author_name}`" @update:model-value="toggle(c.id, $event === true)" />
            </TableCell>
            <TableCell class="max-w-[16rem] whitespace-normal sm:max-w-xl">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                <span class="font-medium">{{ c.author_name }}</span>
                <Badge v-if="c.staff" variant="secondary">
                  Staff
                </Badge>
                <span class="truncate font-mono text-xs text-muted-foreground">{{ c.author_email }}</span>
              </div>
              <div class="mt-0.5 flex flex-wrap gap-x-3 font-mono text-[11px] text-muted-foreground">
                <span>{{ fmt.format(new Date(c.created_at)) }}</span>
                <span v-if="c.ip">{{ c.ip }}</span>
                <a v-if="c.author_url" :href="c.author_url" target="_blank" rel="nofollow noopener noreferrer" class="truncate underline">{{ c.author_url }}</a>
              </div>
              <p v-if="c.parent_author" class="mt-2 text-xs text-muted-foreground">
                Reply to {{ c.parent_author }}
              </p>
              <p class="mt-1 text-sm break-words whitespace-pre-line">
                {{ c.body }}
              </p>
              <p class="mt-1 truncate text-xs text-muted-foreground lg:hidden">
                On {{ c.post_title }}
              </p>
            </TableCell>
            <TableCell class="hidden max-w-xs lg:table-cell">
              <a v-if="c.post_url" :href="c.post_url" target="_blank" rel="noopener" class="line-clamp-2 text-sm underline-offset-2 hover:underline">{{ c.post_title }}</a>
              <span v-else class="line-clamp-2 text-sm">{{ c.post_title }}</span>
            </TableCell>
            <TableCell class="hidden md:table-cell">
              <Popover v-if="c.spam_reasons.length">
                <PopoverTrigger as-child>
                  <Button variant="ghost" size="sm" class="font-mono text-xs">
                    {{ c.spam_score ?? 0 }}
                  </Button>
                </PopoverTrigger>
                <PopoverContent align="start" class="w-72">
                  <p class="mb-2 font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
                    Why
                  </p>
                  <ul class="grid gap-1 text-sm">
                    <li v-for="(r, i) in c.spam_reasons" :key="i" class="flex justify-between gap-3">
                      <span>{{ r.check.replaceAll('_', ' ') }}<span v-if="r.detail" class="text-muted-foreground"> · {{ r.detail }}</span></span>
                      <span class="font-mono text-xs">{{ r.delta > 0 ? `+${r.delta}` : r.delta }}</span>
                    </li>
                  </ul>
                </PopoverContent>
              </Popover>
              <span v-else class="font-mono text-xs text-muted-foreground">{{ c.spam_score ?? '—' }}</span>
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(c)" :label="`Actions for comment by ${c.author_name}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="5">
            No {{ status }} comments.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <ConfirmDialog
      v-model:open="confirmOpen"
      title="Delete permanently"
      :description="`Delete ${pendingDelete.length} comment${pendingDelete.length === 1 ? '' : 's'} for good? Replies to them become top-level comments.`"
      action="Delete"
      @confirm="apply('delete', pendingDelete)"
    />

    <Dialog v-model:open="replyOpen">
      <DialogContent class="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Reply to {{ replyTo?.author_name }}</DialogTitle>
          <DialogDescription>
            Published right away under your name on “{{ replyTo?.post_title }}”.
          </DialogDescription>
        </DialogHeader>
        <p class="max-h-32 overflow-y-auto rounded-md bg-muted p-3 text-sm whitespace-pre-line">
          {{ replyTo?.body }}
        </p>
        <Textarea v-model="replyBody" rows="5" maxlength="5000" placeholder="Your reply" />
        <DialogFooter>
          <Button :disabled="replying || replyBody.trim().length < 2" @click="sendReply">
            Publish reply
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
