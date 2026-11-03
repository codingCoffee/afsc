<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { API } from '~/composables/useAuth'

definePageMeta({ layout: 'auth' })
useHead({ title: 'Accept invite · AFSC' })

interface Invite {
  email: string
  username: string
  display_name: string
  expires_at: string
}

const route = useRoute()
const { startSession, user } = useAuth()
const token = computed(() => String(route.params.token))

const invite = ref<Invite | null>(null)
const loadError = ref('')
const password = ref('')
const fieldError = ref('')
const error = ref('')
const loading = ref(false)
const pw = ref<{ ok: boolean }>()

onMounted(async () => {
  try {
    invite.value = await $fetch<Invite>(`${API}/invites/${encodeURIComponent(token.value)}`)
  }
  catch (e) {
    loadError.value = statusOf(e) === 429 ? 'Too many attempts. Wait a while and try again.' : 'This invite link is invalid, used or expired. Ask an admin for a new one.'
  }
})

async function submit() {
  error.value = ''
  fieldError.value = ''
  loading.value = true
  try {
    await startSession(`${API}/invites/${encodeURIComponent(token.value)}`, { password: password.value })
    await navigateTo('/dashboard')
  }
  catch (e) {
    const { message, field } = errorData(e)
    if (field)
      fieldError.value = message ?? 'Invalid password'
    else
      error.value = message ?? 'Something went wrong.'
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <AuthShell code="§ invite — 01" title="Welcome.">
    <p v-if="user" class="mb-5 text-sm text-muted-foreground">
      You're signed in as {{ user.username }}. Accepting signs you in as the invited account.
    </p>
    <FormError v-if="loadError" :message="loadError" />
    <form v-else-if="invite" class="grid gap-5" novalidate @submit.prevent="submit">
      <dl class="grid gap-1 font-mono text-xs">
        <div class="flex gap-2">
          <dt class="text-muted-foreground">
            Email
          </dt>
          <dd>{{ invite.email }}</dd>
        </div>
        <div class="flex gap-2">
          <dt class="text-muted-foreground">
            Username
          </dt>
          <dd>{{ invite.username }}</dd>
        </div>
      </dl>
      <PasswordField id="password" ref="pw" v-model="password" label="Choose a password" :error="fieldError" />
      <FormError :message="error" />
      <Button type="submit" size="lg" class="font-mono text-xs tracking-widest uppercase" :disabled="loading || !pw?.ok">
        {{ loading ? 'Saving' : 'Set password and sign in' }}
        <ArrowRight />
      </Button>
    </form>
    <p v-else class="font-mono text-xs text-muted-foreground">
      Checking the link…
    </p>
  </AuthShell>
</template>
