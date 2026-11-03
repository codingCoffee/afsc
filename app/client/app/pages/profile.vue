<script setup lang="ts">
import type { SessionRow } from '@/components/SessionList.vue'
import { reactive, ref } from 'vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { API } from '~/composables/useAuth'

definePageMeta({ middleware: 'auth' })
useHead({ title: 'Profile · AFSC' })

const { user, authFetch } = useAuth()

const profile = reactive({
  first_name: user.value?.first_name ?? '',
  last_name: user.value?.last_name ?? '',
  display_name: user.value?.display_name ?? '',
})
const profileErrors = ref<Record<string, string>>({})

async function saveProfile() {
  profileErrors.value = {}
  try {
    user.value = await authFetch(`${API}/me`, { method: 'PATCH', body: { ...profile } })
    toast.success('Saved.')
  }
  catch (e) {
    const { message, field } = errorData(e)
    profileErrors.value = { [field ?? 'form']: message ?? 'Could not save.' }
  }
}

const pw = reactive({ current: '', new: '' })
const pwErrors = ref<Record<string, string>>({})
const pwField = ref<{ ok: boolean }>()

async function changePassword() {
  pwErrors.value = {}
  try {
    await authFetch(`${API}/me/password`, { method: 'POST', body: { ...pw } })
    pw.current = ''
    pw.new = ''
    toast.success('Password changed. Your other sessions were signed out.')
    loadSessions()
  }
  catch (e) {
    const { message, field } = errorData(e)
    pwErrors.value = { [field ?? 'form']: statusOf(e) === 429 ? 'Too many attempts. Wait a while.' : message ?? 'Could not change it.' }
  }
}

const sessions = ref<SessionRow[]>([])
async function loadSessions() {
  sessions.value = (await authFetch<{ items: SessionRow[] }>(`${API}/me/sessions`)).items
}
async function revokeOthers() {
  const res = await authFetch<{ revoked: number }>(`${API}/me/sessions/revoke-others`, { method: 'POST' })
  toast.success(res.revoked ? 'Other sessions signed out.' : 'No other sessions.')
  loadSessions()
}
loadSessions()

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <div class="grid max-w-2xl gap-10">
    <PageHeader title="Profile" :description="user ? `${user.username} · ${user.email} · ${user.role}` : ''" />

    <form class="grid gap-4" @submit.prevent="saveProfile">
      <h2 class="font-display text-sm tracking-wide uppercase">
        Name
      </h2>
      <div class="grid gap-4 sm:grid-cols-2">
        <div class="grid gap-2">
          <Label for="first" :class="labelClass">First name</Label>
          <Input id="first" v-model="profile.first_name" autocomplete="given-name" />
        </div>
        <div class="grid gap-2">
          <Label for="last" :class="labelClass">Last name</Label>
          <Input id="last" v-model="profile.last_name" autocomplete="family-name" />
        </div>
      </div>
      <div class="grid gap-2">
        <Label for="display" :class="labelClass">Display name</Label>
        <Input id="display" v-model="profile.display_name" />
      </div>
      <FormError :message="Object.values(profileErrors)[0]" />
      <Button type="submit" size="sm" class="justify-self-start">
        Save
      </Button>
    </form>

    <form class="grid gap-4" @submit.prevent="changePassword">
      <h2 class="font-display text-sm tracking-wide uppercase">
        Password
      </h2>
      <div class="grid gap-2">
        <Label for="current" :class="labelClass">Current password</Label>
        <Input id="current" v-model="pw.current" type="password" autocomplete="current-password" :aria-invalid="!!pwErrors.current" />
        <FormError :message="pwErrors.current" />
      </div>
      <PasswordField id="new" ref="pwField" v-model="pw.new" label="New password" :error="pwErrors.new" />
      <FormError :message="pwErrors.form" />
      <Button type="submit" size="sm" class="justify-self-start" :disabled="!pw.current || !pwField?.ok">
        Change password
      </Button>
    </form>

    <section class="grid gap-4">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 class="font-display text-sm tracking-wide uppercase">
          Sessions
        </h2>
        <Button variant="outline" size="sm" :disabled="sessions.length < 2" @click="revokeOthers">
          Sign out other sessions
        </Button>
      </div>
      <SessionList :sessions="sessions" />
    </section>
  </div>
</template>
