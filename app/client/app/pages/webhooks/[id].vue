<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { WebhookDelivery, WebhookDeliveryDetail, WebhookDeliveryStatus, WebhookEndpoint, WebhookEvent } from '@/types/api'
import { ArrowLeft, RotateCcw } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { API, errorData } from '@/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'settings.manage' })

const route = useRoute()
const id = computed(() => Number(route.params.id))
const { authFetch } = useAuth()
const base = `${API}/plugins/webhooks`

const endpoint = ref<WebhookEndpoint | null>(null)
const events = ref<string[]>([])
useHead(() => ({ title: `${endpoint.value?.description || 'Deliveries'} · Webhooks · AFSC` }))

const page = ref(1)
const status = ref<WebhookDeliveryStatus | ''>('')
const event = ref('')
const data = ref<Page<WebhookDelivery> | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<WebhookDelivery>>(`${base}/deliveries`, {
      query: { endpoint_id: id.value, status: status.value, event: event.value, page: page.value },
    })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load deliveries.'
  }
  finally {
    loading.value = false
  }
}

async function loadEndpoint() {
  try {
    const [eps, evs] = await Promise.all([
      authFetch<{ items: WebhookEndpoint[] }>(`${base}/endpoints`),
      authFetch<{ events: WebhookEvent[] }>(`${base}/events`),
    ])
    endpoint.value = eps.items.find(e => e.id === id.value) ?? null
    events.value = [...evs.events.map(e => e.name), 'ping']
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load the endpoint.'
  }
}

watch([status, event], () => {
  page.value = 1
  load()
})
watch(page, load)
load()
loadEndpoint()

const detail = ref<WebhookDeliveryDetail | null>(null)
async function open(d: WebhookDelivery) {
  try {
    detail.value = await authFetch<WebhookDeliveryDetail>(`${base}/deliveries/${d.id}`)
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not load the delivery.')
  }
}

async function redeliver(d: WebhookDelivery) {
  try {
    await authFetch(`${base}/deliveries/${d.id}/redeliver`, { method: 'POST' })
    toast.success('Queued again.')
    detail.value = null
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not redeliver.')
  }
}

const statuses: WebhookDeliveryStatus[] = ['pending', 'succeeded', 'failed', 'cancelled']
const statusVariant = (s: string) => s === 'failed' ? 'destructive' : s === 'succeeded' ? 'secondary' : 'outline'
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const pretty = (v: unknown) => JSON.stringify(v, null, 2)
const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Deliveries" :description="endpoint?.url ?? ''">
      <template #actions>
        <Button variant="outline" size="sm" as-child>
          <NuxtLink to="/webhooks">
            <ArrowLeft />
            Endpoints
          </NuxtLink>
        </Button>
      </template>
    </PageHeader>

    <div class="flex flex-wrap gap-2">
      <NativeSelect v-model="status" aria-label="Status">
        <NativeSelectOption value="">
          Any status
        </NativeSelectOption>
        <NativeSelectOption v-for="s in statuses" :key="s" :value="s">
          {{ s }}
        </NativeSelectOption>
      </NativeSelect>
      <NativeSelect v-model="event" aria-label="Event">
        <NativeSelectOption value="">
          Any event
        </NativeSelectOption>
        <NativeSelectOption v-for="e in events" :key="e" :value="e">
          {{ e }}
        </NativeSelectOption>
      </NativeSelect>
    </div>

    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>Event</TableHead>
            <TableHead>Status</TableHead>
            <TableHead class="hidden text-right sm:table-cell">
              Code
            </TableHead>
            <TableHead class="hidden text-right md:table-cell">
              Attempts
            </TableHead>
            <TableHead class="hidden md:table-cell">
              Created
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="d in data?.items ?? []" :key="d.id" class="cursor-pointer" @click="open(d)">
            <TableCell>
              <button type="button" class="text-left font-mono text-xs hover:underline" @click.stop="open(d)">
                {{ d.event }}
              </button>
              <div class="font-mono text-[10px] text-muted-foreground md:hidden">
                {{ fmt.format(new Date(d.created_at)) }}
              </div>
            </TableCell>
            <TableCell>
              <Badge :variant="statusVariant(d.status)" class="rounded-sm text-[10px] uppercase">
                {{ d.status }}
              </Badge>
            </TableCell>
            <TableCell class="hidden text-right font-mono text-xs sm:table-cell">
              {{ d.last_status_code ?? '—' }}
            </TableCell>
            <TableCell class="hidden text-right font-mono text-xs md:table-cell">
              {{ d.attempts }}
            </TableCell>
            <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
              {{ fmt.format(new Date(d.created_at)) }}
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="5">
            No deliveries in the last 30 days.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="data.page_size" />

    <Sheet :open="!!detail" @update:open="(v: boolean) => { if (!v) detail = null }">
      <SheetContent class="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader>
          <SheetTitle class="font-mono">
            {{ detail?.event }}
          </SheetTitle>
          <SheetDescription>
            Delivery {{ detail?.id }}<span v-if="detail?.redelivery_of">, redelivery of {{ detail.redelivery_of }}</span>
          </SheetDescription>
        </SheetHeader>
        <div v-if="detail" class="grid gap-5 px-4 pb-6 text-sm">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
            <dt :class="labelClass">
              Status
            </dt>
            <dd>
              <Badge :variant="statusVariant(detail.status)" class="rounded-sm text-[10px] uppercase">
                {{ detail.status }}
              </Badge>
            </dd>
            <dt :class="labelClass">
              Attempts
            </dt>
            <dd class="font-mono text-xs">
              {{ detail.attempts }}
            </dd>
            <dt :class="labelClass">
              Response
            </dt>
            <dd class="font-mono text-xs">
              {{ detail.last_status_code ?? '—' }}<span v-if="detail.duration_ms !== null"> · {{ detail.duration_ms }} ms</span>
            </dd>
            <template v-if="detail.last_error">
              <dt :class="labelClass">
                Error
              </dt>
              <dd class="text-xs break-words text-destructive">
                {{ detail.last_error }}
              </dd>
            </template>
            <dt :class="labelClass">
              Event ID
            </dt>
            <dd class="font-mono text-xs break-all">
              {{ detail.event_id }}
            </dd>
          </dl>
          <section class="grid gap-2">
            <h3 :class="labelClass">
              Request headers
            </h3>
            <pre class="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">{{ Object.entries(detail.request_headers).map(([k, v]) => `${k}: ${v}`).join('\n') }}</pre>
          </section>
          <section class="grid gap-2">
            <h3 :class="labelClass">
              Payload
            </h3>
            <pre class="max-h-80 overflow-auto rounded-md bg-muted p-3 font-mono text-xs">{{ pretty(detail.payload) }}</pre>
          </section>
          <section v-if="detail.response_snippet" class="grid gap-2">
            <h3 :class="labelClass">
              Response (first 1 KB)
            </h3>
            <pre class="max-h-60 overflow-auto rounded-md bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{{ detail.response_snippet }}</pre>
          </section>
          <Button variant="outline" size="sm" class="justify-self-start" @click="redeliver(detail)">
            <RotateCcw />
            Redeliver
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  </div>
</template>
