<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import { ref, watch } from 'vue'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'audit.read' })
useHead({ title: 'Audit log · AFSC' })

interface Entry {
  id: number
  actor_id: string | null
  actor_name: string | null
  action: string
  entity_type: string
  entity_id: string
  diff: unknown
  ip: string | null
  created_at: string
}

const PAGE_SIZE = 50
const { authFetch } = useAuth()
const page = ref(1)
const action = ref('')
const entityType = ref('')
const entityID = ref('')
const from = ref('')
const to = ref('')
const data = ref<Page<Entry> | null>(null)
const error = ref('')
const open = ref<number | null>(null)
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' })

function iso(d: string) {
  return d ? new Date(d).toISOString() : undefined
}

async function load() {
  error.value = ''
  try {
    data.value = await authFetch<Page<Entry>>('/admin/api/v1/audit', {
      query: { page: page.value, page_size: PAGE_SIZE, action: action.value, entity_type: entityType.value, entity_id: entityID.value, from: iso(from.value), to: iso(to.value) },
    })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load the audit log.'
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([action, entityType, entityID, from, to], () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    page.value = 1
    load()
  }, 300)
})
watch(page, load)
load()
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Audit log" description="Read-only. Sign-ins, user and role changes, settings, publishing and redirect changes." />
    <div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
      <Input v-model="action" placeholder="Action, e.g. login. or post.publish" aria-label="Action" />
      <Input v-model="entityType" placeholder="Entity type" aria-label="Entity type" />
      <Input v-model="entityID" placeholder="Entity id" aria-label="Entity id" />
      <Input v-model="from" type="datetime-local" aria-label="From" />
      <Input v-model="to" type="datetime-local" aria-label="To" />
    </div>
    <FormError :message="error" />
    <div class="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>When</TableHead>
            <TableHead>Action</TableHead>
            <TableHead class="hidden md:table-cell">
              Actor
            </TableHead>
            <TableHead class="hidden md:table-cell">
              Entity
            </TableHead>
            <TableHead class="hidden lg:table-cell">
              IP
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-for="e in data?.items ?? []" :key="e.id">
            <TableRow class="cursor-pointer" @click="open = open === e.id ? null : e.id">
              <TableCell class="font-mono text-xs whitespace-nowrap">
                {{ fmt.format(new Date(e.created_at)) }}
              </TableCell>
              <TableCell class="font-mono text-xs">
                {{ e.action }}
              </TableCell>
              <TableCell class="hidden md:table-cell">
                {{ e.actor_name ?? (e.actor_id ? 'deleted user' : 'system') }}
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
                {{ e.entity_type }}{{ e.entity_id ? ` ${e.entity_id}` : '' }}
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground lg:table-cell">
                {{ e.ip ?? '—' }}
              </TableCell>
            </TableRow>
            <TableRow v-if="open === e.id">
              <TableCell colspan="5">
                <pre class="max-h-80 overflow-auto rounded bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{{ e.diff ? JSON.stringify(e.diff, null, 2) : 'No details.' }}</pre>
              </TableCell>
            </TableRow>
          </template>
          <TableEmpty v-if="data && !data.items.length" :colspan="5">
            Nothing matches.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />
  </div>
</template>
