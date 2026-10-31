<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import { Ban, Eraser } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

interface Token {
  id: string
  session_id: string
  user_id: string
  username: string
  user_agent: string
  ip: string
  created_at: string
  expires_at: string
  revoked_at: string | null
  status: 'active' | 'expired' | 'revoked'
  current: boolean
}

definePageMeta({ middleware: 'auth' })
useHead({ title: 'Tokens · AFSC' })

const PAGE_SIZE = 20

const { user: me, authFetch, signout } = useAuth()
const isAdmin = computed(() => me.value?.role === 'admin')

const filter = ref<'active' | 'all'>('active')
const page = ref(1)
const data = ref<Page<Token> | null>(null)
const loading = ref(false)
const error = ref('')
const notice = ref('')
const pending = ref<Token | null>(null)
const blacklistOpen = ref(false)
const cleanupOpen = ref(false)

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

const statusClass: Record<Token['status'], string> = {
  active: 'border-ok/40 text-ok',
  expired: 'text-muted-foreground',
  revoked: 'border-destructive/40 text-destructive',
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<Token>>(`/admin/api/v1/tokens?status=${filter.value}&page=${page.value}&page_size=${PAGE_SIZE}`)
    if (!data.value.items.length && page.value > 1)
      page.value--
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load tokens.'
  }
  finally {
    loading.value = false
  }
}

function askBlacklist(t: Token) {
  pending.value = t
  blacklistOpen.value = true
}

async function confirmBlacklist() {
  const target = pending.value
  if (!target)
    return
  error.value = ''
  try {
    await authFetch(`/admin/api/v1/tokens/${target.id}/blacklist`, { method: 'POST' })
    if (target.current)
      return signout()
    notice.value = `Blacklisted a session for @${target.username}.`
    await load()
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not blacklist token.'
  }
}

async function confirmCleanup() {
  error.value = ''
  try {
    const { deleted } = await authFetch<{ deleted: number }>('/admin/api/v1/tokens/expired', { method: 'DELETE' })
    notice.value = `Removed ${deleted} expired token${deleted === 1 ? '' : 's'}.`
    await load()
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Cleanup failed.'
  }
}

function setFilter(value: unknown) {
  if (value !== 'active' && value !== 'all')
    return
  filter.value = value
  if (page.value === 1)
    load()
  else
    page.value = 1
}

watch(page, load)
watch(isAdmin, (admin) => {
  if (admin)
    load()
}, { immediate: true })
</script>

<template>
  <Forbidden v-if="!isAdmin" />

  <section v-else class="bg-grid border-b">
    <div class="mx-auto max-w-7xl px-4 pt-12 pb-20 sm:px-8 sm:pt-16">
      <div class="flex flex-col gap-6 lg:flex-row lg:items-end lg:justify-between">
        <div class="animate-rise">
          <p class="font-mono text-[11px] tracking-widest text-primary uppercase">
            § model / tokens
          </p>
          <h1 class="mt-4 font-display text-4xl tracking-tight uppercase sm:text-5xl">
            Tokens.
          </h1>
          <p class="mt-3 max-w-md text-sm text-muted-foreground">
            One row per issued refresh token. Blacklisting ends that whole session immediately.
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <ToggleGroup
            :model-value="filter"
            type="single"
            variant="outline"
            aria-label="Filter tokens"
            @update:model-value="setFilter"
          >
            <ToggleGroupItem value="active" class="px-3 font-mono text-xs tracking-widest uppercase">
              Outstanding
            </ToggleGroupItem>
            <ToggleGroupItem value="all" class="px-3 font-mono text-xs tracking-widest uppercase">
              All
            </ToggleGroupItem>
          </ToggleGroup>
          <Button variant="outline" class="font-mono text-xs tracking-widest uppercase" @click="cleanupOpen = true">
            <Eraser />
            Clean up expired
          </Button>
        </div>
      </div>

      <p v-if="error" role="alert" class="mt-8 font-mono text-xs text-destructive">
        {{ error }}
      </p>
      <p v-else-if="notice" role="status" class="mt-8 font-mono text-xs text-ok">
        {{ notice }}
      </p>

      <p class="mt-8 font-mono text-xs text-muted-foreground">
        {{ data ? `${data.total} ${filter === 'active' ? 'outstanding' : 'total'}` : 'Loading…' }}
      </p>

      <div class="mt-3 rounded-md border bg-card animate-rise [animation-delay:120ms]" :class="{ 'opacity-60': loading }">
        <Table>
          <TableHeader>
            <TableRow class="font-mono text-[10px] tracking-widest uppercase">
              <TableHead>User</TableHead>
              <TableHead>Status</TableHead>
              <TableHead class="hidden md:table-cell">
                Issued
              </TableHead>
              <TableHead class="hidden sm:table-cell">
                Expires
              </TableHead>
              <TableHead class="hidden lg:table-cell">
                IP
              </TableHead>
              <TableHead class="hidden xl:table-cell">
                Device
              </TableHead>
              <TableHead class="w-12">
                <span class="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="t in data?.items ?? []" :key="t.id">
              <TableCell class="font-mono text-xs">
                @{{ t.username }}
              </TableCell>
              <TableCell>
                <div class="flex flex-wrap items-center gap-1.5">
                  <Badge variant="outline" class="rounded-sm font-mono text-[10px] tracking-widest uppercase" :class="statusClass[t.status]">
                    {{ t.status }}
                  </Badge>
                  <Badge v-if="t.current" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
                    this session
                  </Badge>
                </div>
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
                {{ fmt.format(new Date(t.created_at)) }}
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground sm:table-cell">
                {{ fmt.format(new Date(t.expires_at)) }}
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground lg:table-cell">
                {{ t.ip || '—' }}
              </TableCell>
              <TableCell class="hidden max-w-64 truncate text-xs text-muted-foreground xl:table-cell" :title="t.user_agent">
                {{ t.user_agent || '—' }}
              </TableCell>
              <TableCell>
                <Button
                  v-if="t.status === 'active'"
                  variant="ghost"
                  size="icon-sm"
                  class="text-muted-foreground hover:text-destructive"
                  :aria-label="`Blacklist session for @${t.username}`"
                  @click="askBlacklist(t)"
                >
                  <Ban />
                </Button>
              </TableCell>
            </TableRow>
            <TableEmpty v-if="data && !data.items.length" :colspan="7">
              {{ filter === 'active' ? 'No outstanding tokens.' : 'No tokens.' }}
            </TableEmpty>
          </TableBody>
        </Table>
      </div>

      <div class="mt-6">
        <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />
      </div>
    </div>

    <ConfirmDialog
      v-model:open="blacklistOpen"
      :title="pending?.current ? 'Blacklist your own session?' : `Blacklist @${pending?.username ?? ''}'s session?`"
      :description="pending?.current
        ? 'This is the session you are using right now. You will be signed out immediately.'
        : 'The session ends immediately. They will need to sign in again on that device.'"
      action="Blacklist"
      @confirm="confirmBlacklist"
    />
    <ConfirmDialog
      v-model:open="cleanupOpen"
      title="Clean up expired tokens?"
      description="Permanently deletes every token past its expiry. Revoked tokens that haven't expired are kept so token replay can still be detected."
      action="Delete expired"
      @confirm="confirmCleanup"
    />
  </section>
</template>
