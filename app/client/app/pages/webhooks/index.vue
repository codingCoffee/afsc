<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { WebhookEndpoint, WebhookEvent, WebhookSecret } from '@/types/api'
import { Copy, History, KeyRound, Pencil, Plus, Power, Send, Trash2, TriangleAlert } from '@lucide/vue'
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import WebhookEndpointDialog from '@/components/webhooks/WebhookEndpointDialog.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { API, errorData } from '@/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'settings.manage' })
useHead({ title: 'Webhooks · AFSC' })

const { authFetch } = useAuth()
const base = `${API}/plugins/webhooks`

const endpoints = ref<WebhookEndpoint[] | null>(null)
const events = ref<WebhookEvent[]>([])
const loading = ref(false)
const error = ref('')
const off = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [eps, evs] = await Promise.all([
      authFetch<{ items: WebhookEndpoint[] }>(`${base}/endpoints`),
      events.value.length ? Promise.resolve({ events: events.value }) : authFetch<{ events: WebhookEvent[] }>(`${base}/events`),
    ])
    endpoints.value = eps.items
    events.value = evs.events
  }
  catch (e) {
    off.value = (e as { statusCode?: number }).statusCode === 404
    error.value = off.value ? '' : errorData(e).message ?? 'Could not load webhooks.'
  }
  finally {
    loading.value = false
  }
}
load()

const dialogOpen = ref(false)
const editing = ref<WebhookEndpoint | null>(null)
function openEditor(e: WebhookEndpoint | null) {
  editing.value = e
  dialogOpen.value = true
}

const secret = ref('')
const secretOpen = ref(false)
function showSecret(s: WebhookSecret) {
  secret.value = s.secret
  secretOpen.value = true
  load()
}
async function copy(text: string) {
  await navigator.clipboard.writeText(text)
  toast.success('Copied.')
}

async function act(fn: () => Promise<unknown>, done: string) {
  try {
    await fn()
    toast.success(done)
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Something went wrong.')
  }
}

const confirm = ref<{ kind: 'rotate' | 'delete', endpoint: WebhookEndpoint } | null>(null)
const confirmOpen = ref(false)
function ask(kind: 'rotate' | 'delete', endpoint: WebhookEndpoint) {
  confirm.value = { kind, endpoint }
  confirmOpen.value = true
}
async function confirmed() {
  const c = confirm.value
  if (!c)
    return
  if (c.kind === 'delete') {
    await act(() => authFetch(`${base}/endpoints/${c.endpoint.id}`, { method: 'DELETE' }), 'Endpoint deleted.')
    return
  }
  try {
    showSecret(await authFetch<WebhookSecret>(`${base}/endpoints/${c.endpoint.id}/rotate-secret`, { method: 'POST' }))
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not rotate the secret.')
  }
}

function actions(e: WebhookEndpoint): Action[] {
  return [
    { label: 'Edit', icon: Pencil, run: () => openEditor(e) },
    { label: 'Deliveries', icon: History, run: () => navigateTo(`/webhooks/${e.id}`) },
    { label: 'Send test', icon: Send, disabled: !e.enabled, run: () => act(() => authFetch(`${base}/endpoints/${e.id}/ping`, { method: 'POST' }), 'Test event queued.') },
    {
      label: e.enabled ? 'Disable' : 'Enable',
      icon: Power,
      run: () => act(() => authFetch(`${base}/endpoints/${e.id}`, {
        method: 'PUT',
        body: { url: e.url, description: e.description, events: e.events, enabled: !e.enabled },
      }), e.enabled ? 'Endpoint disabled.' : 'Endpoint enabled.'),
    },
    { label: 'Rotate secret', icon: KeyRound, run: () => ask('rotate', e) },
    { label: 'Delete', icon: Trash2, danger: true, run: () => ask('delete', e) },
  ]
}

const statusVariant = (s: string | null) => s === 'failed' ? 'destructive' : s === 'succeeded' ? 'secondary' : 'outline'
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Webhooks" description="Signed POSTs to your services when content, comments, media, logins or settings change.">
      <template v-if="!off" #actions>
        <Button size="sm" @click="openEditor(null)">
          <Plus />
          New endpoint
        </Button>
      </template>
    </PageHeader>

    <p v-if="off" class="text-sm text-muted-foreground">
      The webhooks plugin is switched off. Turn it on under
      <NuxtLink to="/settings/plugins" class="underline underline-offset-4">
        Settings → Plugins
      </NuxtLink>.
    </p>
    <FormError :message="error" />

    <div v-if="!off" class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>Endpoint</TableHead>
            <TableHead class="hidden md:table-cell">
              Events
            </TableHead>
            <TableHead>Last</TableHead>
            <TableHead class="hidden text-right sm:table-cell">
              Failed 24 h
            </TableHead>
            <TableHead class="w-12">
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="e in endpoints ?? []" :key="e.id" :class="{ 'text-muted-foreground': !e.enabled }">
            <TableCell class="max-w-[14rem] sm:max-w-sm">
              <NuxtLink :to="`/webhooks/${e.id}`" class="block truncate font-mono text-xs hover:underline" :title="e.url">
                {{ e.url }}
              </NuxtLink>
              <div class="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                <span v-if="e.description" class="truncate">{{ e.description }}</span>
                <Badge v-if="!e.enabled" variant="outline" class="rounded-sm text-[10px] uppercase">
                  off
                </Badge>
                <span v-if="e.secret_unreadable" class="flex items-center gap-1 text-destructive">
                  <TriangleAlert class="size-3" /> rotate secret
                </span>
                <span class="font-mono md:hidden">{{ e.events.length }} events</span>
              </div>
            </TableCell>
            <TableCell class="hidden max-w-xs md:table-cell">
              <div class="truncate font-mono text-xs" :title="e.events.join(', ')">
                {{ e.events.join(', ') }}
              </div>
            </TableCell>
            <TableCell>
              <Badge v-if="e.last_status" :variant="statusVariant(e.last_status)" class="rounded-sm text-[10px] uppercase">
                {{ e.last_status }}
              </Badge>
              <span v-else class="text-xs text-muted-foreground">—</span>
            </TableCell>
            <TableCell class="hidden text-right font-mono text-xs sm:table-cell" :class="{ 'text-destructive': e.failures_24h }">
              {{ e.failures_24h }}
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(e)" :label="`Actions for ${e.url}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="endpoints && !endpoints.length" :colspan="5">
            No endpoints yet.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>

    <WebhookEndpointDialog v-model:open="dialogOpen" :endpoint="editing" :events="events" @created="showSecret" @updated="load" />

    <Dialog v-model:open="secretOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Signing secret</DialogTitle>
          <DialogDescription>
            It's shown only once. Store it with your receiver and verify the X-AFSC-Signature header with it.
          </DialogDescription>
        </DialogHeader>
        <div class="flex gap-2">
          <Input :model-value="secret" readonly class="font-mono text-xs" @focus="($event.target as HTMLInputElement).select()" />
          <Button variant="outline" size="icon" aria-label="Copy secret" @click="copy(secret)">
            <Copy />
          </Button>
        </div>
        <DialogFooter>
          <Button @click="secretOpen = false">
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="confirm?.kind === 'delete' ? 'Delete endpoint?' : 'Rotate secret?'"
      :description="confirm?.kind === 'delete'
        ? `${confirm?.endpoint.url ?? ''} stops receiving events, and its delivery log is deleted.`
        : 'The old secret stops working at once. Deliveries already in flight will fail verification until the receiver has the new one.'"
      :action="confirm?.kind === 'delete' ? 'Delete' : 'Rotate'"
      @confirm="confirmed"
    />
  </div>
</template>
