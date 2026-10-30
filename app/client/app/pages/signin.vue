<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

definePageMeta({ middleware: 'guest' })
useHead({ title: 'Sign in · AFSC' })

const { signin } = useAuth()

const login = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  loading.value = true
  try {
    await signin(login.value, password.value)
    await navigateTo('/dashboard')
  }
  catch (e) {
    error.value = statusOf(e) === 429
      ? 'Too many attempts. Wait a minute and try again.'
      : errorData(e).message ?? 'Something went wrong. Try again.'
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <AuthShell code="§ access — 01" title="Sign in.">
    <form class="grid gap-5" novalidate @submit.prevent="submit">
      <div class="grid gap-2">
        <Label for="login" class="font-mono text-[11px] tracking-widest text-muted-foreground uppercase">Username or email</Label>
        <Input id="login" v-model="login" autocomplete="username" autocapitalize="none" spellcheck="false" required />
      </div>
      <div class="grid gap-2">
        <Label for="password" class="font-mono text-[11px] tracking-widest text-muted-foreground uppercase">Password</Label>
        <Input id="password" v-model="password" type="password" autocomplete="current-password" required />
      </div>

      <p v-if="error" role="alert" class="font-mono text-xs text-destructive">
        {{ error }}
      </p>

      <Button type="submit" size="lg" class="mt-2 font-mono text-xs tracking-widest uppercase" :disabled="loading || !login || !password">
        {{ loading ? 'Signing in' : 'Sign in' }}
        <ArrowRight />
      </Button>
    </form>

    <template #footer>
      No account?
      <NuxtLink to="/signup" class="text-foreground underline-offset-4 hover:text-primary hover:underline">
        Sign up
      </NuxtLink>
    </template>
  </AuthShell>
</template>
