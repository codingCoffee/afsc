<script setup lang="ts">
import type { Action } from '@/components/ActionMenu.vue'
import type { SessionRow } from '@/components/SessionList.vue'
import type { Page, Role } from '@/composables/useAuth'
import { Ban, Check, Copy, Link, LogOut, MonitorSmartphone, Plus, Trash2 } from '@lucide/vue'
import { reactive, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'users.manage' })
useHead({ title: 'Users · AFSC' })

interface AdminUser {
  id: string
  username: string
  email: string
  first_name: string
  last_name: string
  display_name: string
  role: Role
  created_at: string
  last_login_at: string | null
  disabled_at: string | null
  has_password: boolean
  invite_pending: boolean
  sessions: number
}

const API = '/admin/api/v1'
const PAGE_SIZE = 20
const roles: Role[] = ['contributor', 'author', 'editor', 'admin']
const { user: me, authFetch } = useAuth()

const page = ref(1)
const q = ref('')
const roleFilter = ref('')
const data = ref<Page<AdminUser> | null>(null)
const loading = ref(false)
const error = ref('')
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<AdminUser>>(`${API}/users`, { query: { page: page.value, page_size: PAGE_SIZE, q: q.value, role: roleFilter.value } })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load users.'
  }
  finally {
    loading.value = false
  }
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
watch([q, roleFilter], () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    page.value = 1
    load()
  }, 250)
})
watch(page, load)
load()

async function act(fn: () => Promise<unknown>, done: string) {
  try {
    await fn()
    toast.success(done)
    await load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'That didn\'t work.')
  }
}

function setRole(u: AdminUser, role: Role) {
  if (role === u.role)
    return
  act(() => authFetch(`${API}/users/${u.id}`, { method: 'PATCH', body: { role } }), `${u.username} is now ${role}. Their sessions ended.`)
}

// invite dialog: create, then show the link exactly once
const inviteOpen = ref(false)
const form = reactive({ email: '', username: '', display_name: '', role: 'contributor' as Role })
const fieldErrors = ref<Record<string, string>>({})
const inviteLink = ref('')
const saving = ref(false)

function openInvite() {
  Object.assign(form, { email: '', username: '', display_name: '', role: 'contributor' })
  fieldErrors.value = {}
  inviteLink.value = ''
  inviteOpen.value = true
}

async function submitInvite() {
  saving.value = true
  fieldErrors.value = {}
  try {
    const res = await authFetch<{ invite_url: string }>(`${API}/users`, { method: 'POST', body: { ...form } })
    inviteLink.value = res.invite_url
    load()
  }
  catch (e) {
    const { message, field } = errorData(e)
    fieldErrors.value = { [field ?? 'form']: message ?? 'Could not create the user.' }
  }
  finally {
    saving.value = false
  }
}

async function reinvite(u: AdminUser) {
  try {
    const res = await authFetch<{ invite_url: string }>(`${API}/users/${u.id}/invite`, { method: 'POST' })
    Object.assign(form, { email: u.email, username: u.username, display_name: u.display_name, role: u.role })
    inviteLink.value = res.invite_url
    inviteOpen.value = true
    load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not create an invite.')
  }
}

async function copy(text: string) {
  await navigator.clipboard.writeText(text)
  toast.success('Copied.')
}

// sessions sheet
const sessionsFor = ref<AdminUser | null>(null)
const sessions = ref<SessionRow[]>([])
async function openSessions(u: AdminUser) {
  sessionsFor.value = u
  sessions.value = (await authFetch<{ items: SessionRow[] }>(`${API}/users/${u.id}/sessions`)).items
}
async function revokeSession(id?: string) {
  const u = sessionsFor.value
  if (!u)
    return
  await act(() => authFetch(`${API}/users/${u.id}/sessions/revoke`, { method: 'POST', body: id ? { session_id: id } : undefined }), 'Signed out.')
  await openSessions(u)
}

const deleting = ref<AdminUser | null>(null)
const confirmOpen = ref(false)

function actions(u: AdminUser): Action[] {
  const self = u.id === me.value?.id
  return [
    { label: u.has_password ? 'New sign-in link' : 'New invite link', icon: Link, disabled: !!u.disabled_at, run: () => reinvite(u) },
    { label: 'Sessions', icon: MonitorSmartphone, run: () => openSessions(u) },
    u.disabled_at
      ? { label: 'Enable', icon: Check, run: () => act(() => authFetch(`${API}/users/${u.id}/enable`, { method: 'POST' }), 'Enabled.') }
      : { label: 'Disable', icon: Ban, disabled: self, run: () => act(() => authFetch(`${API}/users/${u.id}/disable`, { method: 'POST' }), 'Disabled and signed out.') },
    { label: 'Delete', icon: Trash2, danger: true, disabled: self, run: () => { deleting.value = u; confirmOpen.value = true } },
  ]
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Users" description="Accounts are invite-only. New users get a one-time link that's valid for 72 hours.">
      <template #actions>
        <Button size="sm" @click="openInvite">
          <Plus />
          Invite user
        </Button>
      </template>
    </PageHeader>

    <div class="flex flex-wrap gap-2">
      <Input v-model="q" placeholder="Search name, username or email" class="max-w-xs" />
      <NativeSelect v-model="roleFilter" aria-label="Role">
        <NativeSelectOption value="">
          All roles
        </NativeSelectOption>
        <NativeSelectOption v-for="r in roles" :key="r" :value="r">
          {{ r }}
        </NativeSelectOption>
      </NativeSelect>
    </div>

    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>User</TableHead>
            <TableHead class="hidden md:table-cell">
              Email
            </TableHead>
            <TableHead>Role</TableHead>
            <TableHead class="hidden lg:table-cell">
              Last sign-in
            </TableHead>
            <TableHead class="w-12">
              <span class="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="u in data?.items ?? []" :key="u.id" :class="{ 'opacity-60': u.disabled_at }">
            <TableCell>
              <div class="font-medium">
                {{ u.display_name || u.username }}
              </div>
              <div class="flex flex-wrap items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
                {{ u.username }}
                <Badge v-if="u.id === me?.id" variant="outline" class="rounded-sm text-[10px] uppercase">
                  you
                </Badge>
                <Badge v-if="u.disabled_at" variant="destructive" class="rounded-sm text-[10px] uppercase">
                  disabled
                </Badge>
                <Badge v-else-if="u.invite_pending" variant="secondary" class="rounded-sm text-[10px] uppercase">
                  invited
                </Badge>
              </div>
            </TableCell>
            <TableCell class="hidden text-muted-foreground md:table-cell">
              {{ u.email }}
            </TableCell>
            <TableCell>
              <NativeSelect :model-value="u.role" :aria-label="`Role of ${u.username}`" @update:model-value="setRole(u, $event as Role)">
                <NativeSelectOption v-for="r in roles" :key="r" :value="r">
                  {{ r }}
                </NativeSelectOption>
              </NativeSelect>
            </TableCell>
            <TableCell class="hidden font-mono text-xs text-muted-foreground lg:table-cell">
              {{ u.last_login_at ? fmt.format(new Date(u.last_login_at)) : 'never' }}
            </TableCell>
            <TableCell>
              <ActionMenu :actions="actions(u)" :label="`Actions for ${u.username}`" />
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="5">
            No users match.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />

    <Dialog v-model:open="inviteOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ inviteLink ? 'Send this link' : 'Invite a user' }}</DialogTitle>
          <DialogDescription>
            {{ inviteLink ? 'It is shown only once and works for 72 hours. Send it to the person yourself.' : 'They choose their own password from the link.' }}
          </DialogDescription>
        </DialogHeader>
        <div v-if="inviteLink" class="flex gap-2">
          <Input :model-value="inviteLink" readonly class="font-mono text-xs" @focus="($event.target as HTMLInputElement).select()" />
          <Button variant="outline" size="icon" aria-label="Copy link" @click="copy(inviteLink)">
            <Copy />
          </Button>
        </div>
        <form v-else id="invite-form" class="grid gap-4" @submit.prevent="submitInvite">
          <div class="grid gap-2">
            <Label for="inv-email">Email</Label>
            <Input id="inv-email" v-model="form.email" type="email" required :aria-invalid="!!fieldErrors.email" />
            <FormError :message="fieldErrors.email" />
          </div>
          <div class="grid gap-2">
            <Label for="inv-username">Username <span class="text-muted-foreground">(optional)</span></Label>
            <Input id="inv-username" v-model="form.username" autocapitalize="none" spellcheck="false" :aria-invalid="!!fieldErrors.username" />
            <FormError :message="fieldErrors.username" />
          </div>
          <div class="grid gap-2">
            <Label for="inv-name">Display name</Label>
            <Input id="inv-name" v-model="form.display_name" />
          </div>
          <div class="grid gap-2">
            <Label for="inv-role">Role</Label>
            <NativeSelect id="inv-role" v-model="form.role" class="w-full">
              <NativeSelectOption v-for="r in roles" :key="r" :value="r">
                {{ r }}
              </NativeSelectOption>
            </NativeSelect>
          </div>
          <FormError :message="fieldErrors.form ?? fieldErrors.role" />
        </form>
        <DialogFooter>
          <Button v-if="inviteLink" @click="inviteOpen = false">
            Done
          </Button>
          <Button v-else type="submit" form="invite-form" :disabled="saving || !form.email">
            {{ saving ? 'Creating' : 'Create invite' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Sheet :open="!!sessionsFor" @update:open="(v: boolean) => { if (!v) sessionsFor = null }">
      <SheetContent class="w-full sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>Sessions of {{ sessionsFor?.username }}</SheetTitle>
          <SheetDescription>Signing a session out takes effect on its next request.</SheetDescription>
        </SheetHeader>
        <div class="grid gap-4 px-4">
          <SessionList :sessions="sessions" revocable @revoke="revokeSession" />
          <Button v-if="sessions.length" variant="outline" size="sm" class="justify-self-start" @click="revokeSession()">
            <LogOut />
            Sign out everywhere
          </Button>
        </div>
      </SheetContent>
    </Sheet>

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Delete ${deleting?.username ?? ''}?`"
      description="This removes the account and signs it out everywhere. Their posts stay. It can't be undone; disabling is usually better."
      action="Delete user"
      @confirm="deleting && act(() => authFetch(`${API}/users/${deleting!.id}`, { method: 'DELETE' }), 'Deleted.')"
    />
  </div>
</template>
