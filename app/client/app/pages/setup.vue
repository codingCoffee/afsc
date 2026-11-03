<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { computed, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { API } from '~/composables/useAuth'

definePageMeta({ middleware: 'guest', layout: 'auth' })
useHead({ title: 'Set up · AFSC' })

const { startSession } = useAuth()

if (!(await setupNeeded()))
  await navigateTo('/signin', { replace: true })

const form = reactive({ token: '', username: '', email: '', display_name: '', password: '' })
const fieldErrors = ref<Record<string, string>>({})
const error = ref('')
const loading = ref(false)
const pw = ref<{ ok: boolean }>()

const canSubmit = computed(() => form.token && form.username && form.email && pw.value?.ok && !loading.value)
const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'

async function submit() {
  error.value = ''
  fieldErrors.value = {}
  loading.value = true
  try {
    await startSession(`${API}/setup`, { ...form })
    markSetupDone()
    await navigateTo('/dashboard')
  }
  catch (e) {
    const { message, field } = errorData(e)
    if (statusOf(e) === 404) {
      markSetupDone()
      await navigateTo('/signin')
    }
    else if (field) {
      fieldErrors.value = { [field]: message ?? 'Invalid value' }
    }
    else {
      error.value = statusOf(e) === 429 ? 'Too many attempts. Wait a while and try again.' : message ?? 'Something went wrong.'
    }
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <AuthShell code="§ setup — 00" title="First run.">
    <form class="grid gap-5" novalidate @submit.prevent="submit">
      <p class="text-sm text-muted-foreground">
        This creates the first admin. The setup token is printed in the server log on start, or set with
        <code class="font-mono text-xs">AFSC_SETUP_TOKEN</code>.
      </p>
      <div class="grid gap-2">
        <Label for="token" :class="labelClass">Setup token</Label>
        <Input id="token" v-model="form.token" autocomplete="off" spellcheck="false" :aria-invalid="!!fieldErrors.token" required />
        <FormError :message="fieldErrors.token" />
      </div>
      <div class="grid gap-2">
        <Label for="username" :class="labelClass">Username</Label>
        <Input id="username" v-model="form.username" autocomplete="username" autocapitalize="none" spellcheck="false" :aria-invalid="!!fieldErrors.username" required />
        <p class="font-mono text-[11px]" :class="fieldErrors.username ? 'text-destructive' : 'text-muted-foreground'">
          {{ fieldErrors.username ?? '3–32 chars · a–z 0–9 _ . -' }}
        </p>
      </div>
      <div class="grid gap-2">
        <Label for="email" :class="labelClass">Email</Label>
        <Input id="email" v-model="form.email" type="email" autocomplete="email" :aria-invalid="!!fieldErrors.email" required />
        <FormError :message="fieldErrors.email" />
      </div>
      <div class="grid gap-2">
        <Label for="display_name" :class="labelClass">Display name</Label>
        <Input id="display_name" v-model="form.display_name" autocomplete="name" />
      </div>
      <PasswordField id="password" ref="pw" v-model="form.password" label="Password" :error="fieldErrors.password" />
      <FormError :message="error" />
      <Button type="submit" size="lg" class="mt-2 font-mono text-xs tracking-widest uppercase" :disabled="!canSubmit">
        {{ loading ? 'Creating admin' : 'Create admin' }}
        <ArrowRight />
      </Button>
    </form>
  </AuthShell>
</template>
