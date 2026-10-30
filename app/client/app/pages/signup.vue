<script setup lang="ts">
import { ArrowRight, Check, Minus } from '@lucide/vue'
import { computed, reactive, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

definePageMeta({ middleware: 'guest' })
useHead({ title: 'Sign up · AFSC' })

const { signup } = useAuth()

const strict = !import.meta.dev

const form = reactive({ username: '', email: '', password: '', first_name: '', last_name: '' })
const fieldErrors = ref<Record<string, string>>({})
const error = ref('')
const loading = ref(false)

const rules = computed(() => [
  { label: '8+ characters', ok: [...form.password].length >= 8 },
  { label: 'Uppercase', ok: /\p{Lu}/u.test(form.password) },
  { label: 'Lowercase', ok: /\p{Ll}/u.test(form.password) },
  { label: 'Number', ok: /\p{Nd}/u.test(form.password) },
  { label: 'Special', ok: /[\p{P}\p{S}]/u.test(form.password) },
])

const passwordOk = computed(() => form.password !== '' && (!strict || rules.value.every(r => r.ok)))
const canSubmit = computed(() => form.username && form.email && passwordOk.value && !loading.value)

async function submit() {
  error.value = ''
  fieldErrors.value = {}
  loading.value = true
  try {
    await signup({ ...form })
    await navigateTo('/dashboard')
  }
  catch (e) {
    const { message, field } = errorData(e)
    if (field)
      fieldErrors.value = { [field]: message ?? 'Invalid value' }
    else
      error.value = statusOf(e) === 429 ? 'Too many attempts. Wait a minute and try again.' : message ?? 'Something went wrong. Try again.'
  }
  finally {
    loading.value = false
  }
}

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <AuthShell code="§ access — 00" title="Sign up.">
    <form class="grid gap-5" novalidate @submit.prevent="submit">
      <div class="grid gap-5 sm:grid-cols-2">
        <div class="grid gap-2">
          <Label for="first_name" :class="labelClass">First name</Label>
          <Input id="first_name" v-model="form.first_name" autocomplete="given-name" />
        </div>
        <div class="grid gap-2">
          <Label for="last_name" :class="labelClass">Last name</Label>
          <Input id="last_name" v-model="form.last_name" autocomplete="family-name" />
        </div>
      </div>

      <div class="grid gap-2">
        <Label for="username" :class="labelClass">Username</Label>
        <Input
          id="username"
          v-model="form.username"
          autocomplete="username"
          autocapitalize="none"
          spellcheck="false"
          :aria-invalid="!!fieldErrors.username"
          aria-describedby="username-hint"
          required
        />
        <p id="username-hint" class="font-mono text-[11px]" :class="fieldErrors.username ? 'text-destructive' : 'text-muted-foreground'">
          {{ fieldErrors.username ?? '3–32 chars · a–z 0–9 _ . -' }}
        </p>
      </div>

      <div class="grid gap-2">
        <Label for="email" :class="labelClass">Email</Label>
        <Input id="email" v-model="form.email" type="email" autocomplete="email" :aria-invalid="!!fieldErrors.email" required />
        <p v-if="fieldErrors.email" class="font-mono text-[11px] text-destructive">
          {{ fieldErrors.email }}
        </p>
      </div>

      <div class="grid gap-2">
        <Label for="password" :class="labelClass">Password</Label>
        <Input id="password" v-model="form.password" type="password" autocomplete="new-password" :aria-invalid="!!fieldErrors.password" required />
        <p v-if="fieldErrors.password" class="font-mono text-[11px] text-destructive">
          {{ fieldErrors.password }}
        </p>
        <ul v-if="strict" class="mt-1 flex flex-wrap gap-x-4 gap-y-1.5 font-mono text-[11px] tracking-wide uppercase">
          <li
            v-for="rule in rules"
            :key="rule.label"
            class="flex items-center gap-1.5 transition-colors"
            :class="rule.ok ? 'text-ok' : 'text-muted-foreground'"
          >
            <component :is="rule.ok ? Check : Minus" class="size-3" />
            {{ rule.label }}
          </li>
        </ul>
      </div>

      <p v-if="error" role="alert" class="font-mono text-xs text-destructive">
        {{ error }}
      </p>

      <Button type="submit" size="lg" class="mt-2 font-mono text-xs tracking-widest uppercase" :disabled="!canSubmit">
        {{ loading ? 'Creating account' : 'Create account' }}
        <ArrowRight />
      </Button>
    </form>

    <template #footer>
      Already have an account?
      <NuxtLink to="/signin" class="text-foreground underline-offset-4 hover:text-primary hover:underline">
        Sign in
      </NuxtLink>
    </template>
  </AuthShell>
</template>
