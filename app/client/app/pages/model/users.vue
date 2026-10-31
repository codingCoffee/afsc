<script setup lang="ts">
import type { Page, User } from '@/composables/useAuth'
import { Trash2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: 'auth' })
useHead({ title: 'Users · AFSC' })

const PAGE_SIZE = 20

const { user: me, authFetch } = useAuth()
const isAdmin = computed(() => me.value?.user_type === 'admin')

const page = ref(1)
const data = ref<Page<User> | null>(null)
const loading = ref(false)
const error = ref('')
const notice = ref('')
const pending = ref<User | null>(null)
const confirmOpen = ref(false)

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<User>>(`/admin/api/v1/users?page=${page.value}&page_size=${PAGE_SIZE}`)
    if (!data.value.items.length && page.value > 1)
      page.value--
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load users.'
  }
  finally {
    loading.value = false
  }
}

function askDelete(u: User) {
  pending.value = u
  confirmOpen.value = true
}

async function confirmDelete() {
  const target = pending.value
  if (!target)
    return
  error.value = ''
  try {
    await authFetch(`/admin/api/v1/users/${target.id}`, { method: 'DELETE' })
    notice.value = `Deleted @${target.username}.`
    await load()
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not delete user.'
  }
}

function onCreated(u: User) {
  notice.value = `Created @${u.username}.`
  load()
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
      <div class="flex flex-col gap-6 sm:flex-row sm:items-end sm:justify-between">
        <div class="animate-rise">
          <p class="font-mono text-[11px] tracking-widest text-primary uppercase">
            § model / users
          </p>
          <h1 class="mt-4 font-display text-4xl tracking-tight uppercase sm:text-5xl">
            Users.
          </h1>
          <p class="mt-3 font-mono text-xs text-muted-foreground">
            {{ data ? `${data.total} total` : 'Loading…' }}
          </p>
        </div>
        <CreateUserDialog @created="onCreated" />
      </div>

      <p v-if="error" role="alert" class="mt-8 font-mono text-xs text-destructive">
        {{ error }}
      </p>
      <p v-else-if="notice" role="status" class="mt-8 font-mono text-xs text-ok">
        {{ notice }}
      </p>

      <div class="mt-8 rounded-md border bg-card animate-rise [animation-delay:120ms]" :class="{ 'opacity-60': loading }">
        <Table>
          <TableHeader>
            <TableRow class="font-mono text-[10px] tracking-widest uppercase">
              <TableHead>Username</TableHead>
              <TableHead class="hidden md:table-cell">
                Name
              </TableHead>
              <TableHead class="hidden sm:table-cell">
                Email
              </TableHead>
              <TableHead>Type</TableHead>
              <TableHead class="hidden lg:table-cell">
                Created
              </TableHead>
              <TableHead class="w-12">
                <span class="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="u in data?.items ?? []" :key="u.id">
              <TableCell class="font-mono text-xs">
                @{{ u.username }}
                <Badge v-if="u.id === me?.id" variant="outline" class="ml-2 rounded-sm font-mono text-[10px] tracking-widest uppercase">
                  you
                </Badge>
              </TableCell>
              <TableCell class="hidden md:table-cell">
                {{ [u.first_name, u.last_name].filter(Boolean).join(' ') || '—' }}
              </TableCell>
              <TableCell class="hidden text-muted-foreground sm:table-cell">
                {{ u.email }}
              </TableCell>
              <TableCell>
                <Badge :variant="u.user_type === 'admin' ? 'default' : 'outline'" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
                  {{ u.user_type }}
                </Badge>
              </TableCell>
              <TableCell class="hidden font-mono text-xs text-muted-foreground lg:table-cell">
                {{ fmt.format(new Date(u.created_at)) }}
              </TableCell>
              <TableCell>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  class="text-muted-foreground hover:text-destructive"
                  :disabled="u.id === me?.id"
                  :aria-label="`Delete @${u.username}`"
                  @click="askDelete(u)"
                >
                  <Trash2 />
                </Button>
              </TableCell>
            </TableRow>
            <TableEmpty v-if="data && !data.items.length" :colspan="6">
              No users.
            </TableEmpty>
          </TableBody>
        </Table>
      </div>

      <div class="mt-6">
        <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />
      </div>
    </div>

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Delete @${pending?.username ?? ''}?`"
      description="This removes the account and signs it out everywhere. It can't be undone."
      action="Delete user"
      @confirm="confirmDelete"
    />
  </section>
</template>
