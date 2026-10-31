<script setup lang="ts">
import type { User } from '@/composables/useAuth'
import { Plus } from '@lucide/vue'
import { reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

const emit = defineEmits<{ created: [user: User] }>()

const { authFetch } = useAuth()

const open = ref(false)
const loading = ref(false)
const error = ref('')
const fieldErrors = ref<Record<string, string>>({})
const blank = () => ({ username: '', email: '', password: '', first_name: '', last_name: '', user_type: 'user' as 'user' | 'admin' })
const form = reactive(blank())

watch(open, (value) => {
  if (!value)
    return
  Object.assign(form, blank())
  error.value = ''
  fieldErrors.value = {}
})

async function submit() {
  error.value = ''
  fieldErrors.value = {}
  loading.value = true
  try {
    const user = await authFetch<User>('/admin/api/v1/users', { method: 'POST', body: { ...form } })
    open.value = false
    emit('created', user)
  }
  catch (e) {
    const { message, field } = errorData(e)
    if (field)
      fieldErrors.value = { [field]: message ?? 'Invalid value' }
    else
      error.value = message ?? 'Something went wrong. Try again.'
  }
  finally {
    loading.value = false
  }
}

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <Dialog v-model:open="open">
    <DialogTrigger as-child>
      <Button class="font-mono text-xs tracking-widest uppercase">
        <Plus />
        New user
      </Button>
    </DialogTrigger>
    <DialogContent class="max-h-[90svh] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle class="font-display text-base tracking-wide uppercase">
          New user
        </DialogTitle>
        <DialogDescription>
          They can sign in right away with these details.
        </DialogDescription>
      </DialogHeader>

      <form id="create-user" class="grid gap-4" novalidate @submit.prevent="submit">
        <div class="grid gap-4 sm:grid-cols-2">
          <div class="grid gap-2">
            <Label for="cu-first" :class="labelClass">First name</Label>
            <Input id="cu-first" v-model="form.first_name" :aria-invalid="!!fieldErrors.first_name" />
          </div>
          <div class="grid gap-2">
            <Label for="cu-last" :class="labelClass">Last name</Label>
            <Input id="cu-last" v-model="form.last_name" :aria-invalid="!!fieldErrors.last_name" />
          </div>
        </div>

        <div v-for="f in (['username', 'email', 'password'] as const)" :key="f" class="grid gap-2">
          <Label :for="`cu-${f}`" :class="labelClass">{{ f }}</Label>
          <Input
            :id="`cu-${f}`"
            v-model="form[f]"
            :type="f === 'username' ? 'text' : f"
            :autocomplete="f === 'password' ? 'new-password' : 'off'"
            autocapitalize="none"
            spellcheck="false"
            :aria-invalid="!!fieldErrors[f]"
          />
          <p v-if="fieldErrors[f]" class="font-mono text-[11px] text-destructive">
            {{ fieldErrors[f] }}
          </p>
        </div>

        <div class="grid gap-2">
          <span :class="labelClass">Type</span>
          <ToggleGroup
            v-model="form.user_type"
            type="single"
            variant="outline"
            class="w-full"
            aria-label="User type"
            @update:model-value="(v) => { if (!v) form.user_type = 'user' }"
          >
            <ToggleGroupItem value="user" class="flex-1 font-mono text-xs tracking-widest uppercase">
              User
            </ToggleGroupItem>
            <ToggleGroupItem value="admin" class="flex-1 font-mono text-xs tracking-widest uppercase">
              Admin
            </ToggleGroupItem>
          </ToggleGroup>
        </div>

        <p v-if="error" role="alert" class="font-mono text-xs text-destructive">
          {{ error }}
        </p>
      </form>

      <DialogFooter>
        <Button
          type="submit"
          form="create-user"
          class="font-mono text-xs tracking-widest uppercase"
          :disabled="loading || !form.username || !form.email || !form.password"
        >
          {{ loading ? 'Creating' : 'Create user' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
