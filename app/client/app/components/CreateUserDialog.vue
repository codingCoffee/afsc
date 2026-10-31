<script setup lang="ts">
import type { Role, User } from '@/composables/useAuth'
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
const blank = () => ({ username: '', email: '', password: '', first_name: '', last_name: '', role: 'contributor' as Role })
const roles: Role[] = ['contributor', 'author', 'editor', 'admin']
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
          <span :class="labelClass">Role</span>
          <ToggleGroup
            v-model="form.role"
            type="single"
            variant="outline"
            class="grid w-full grid-cols-2 sm:grid-cols-4"
            aria-label="Role"
            @update:model-value="(v) => { if (!v) form.role = 'contributor' }"
          >
            <ToggleGroupItem v-for="r in roles" :key="r" :value="r" class="font-mono text-xs tracking-widest uppercase">
              {{ r }}
            </ToggleGroupItem>
          </ToggleGroup>
          <p v-if="fieldErrors.role" class="font-mono text-[11px] text-destructive">
            {{ fieldErrors.role }}
          </p>
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
