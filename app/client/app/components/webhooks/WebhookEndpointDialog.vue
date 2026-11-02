<script setup lang="ts">
import type { WebhookEndpoint, WebhookEndpointInput, WebhookEvent, WebhookSecret } from '@/types/api'
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { API, errorData } from '@/composables/useAuth'

const props = defineProps<{ endpoint: WebhookEndpoint | null, events: WebhookEvent[] }>()
const emit = defineEmits<{ created: [WebhookSecret], updated: [WebhookEndpoint] }>()
const open = defineModel<boolean>('open', { default: false })

const { authFetch } = useAuth()
const form = reactive<WebhookEndpointInput>({ url: '', description: '', events: [], enabled: true })
const errors = ref<Record<string, string>>({})
const saving = ref(false)

watch(open, (o) => {
  if (!o)
    return
  const e = props.endpoint
  Object.assign(form, e
    ? { url: e.url, description: e.description, events: [...e.events], enabled: e.enabled }
    : { url: '', description: '', events: [], enabled: true })
  errors.value = {}
})

const allSelected = computed(() => props.events.length > 0 && form.events.length === props.events.length)
function toggleAll(on: boolean) {
  form.events = on ? props.events.map(e => e.name) : []
}
function toggle(name: string, on: boolean) {
  form.events = on ? [...form.events, name] : form.events.filter(n => n !== name)
}

async function save() {
  saving.value = true
  errors.value = {}
  try {
    if (props.endpoint) {
      const ep = await authFetch<WebhookEndpoint>(`${API}/plugins/webhooks/endpoints/${props.endpoint.id}`, { method: 'PUT', body: form })
      emit('updated', ep)
    }
    else {
      emit('created', await authFetch<WebhookSecret>(`${API}/plugins/webhooks/endpoints`, { method: 'POST', body: form }))
    }
    open.value = false
  }
  catch (e) {
    const data = errorData(e)
    errors.value = { [data.field ?? 'form']: data.message ?? 'Could not save the endpoint.' }
  }
  finally {
    saving.value = false
  }
}

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ endpoint ? 'Edit endpoint' : 'New endpoint' }}</DialogTitle>
        <DialogDescription>AFSC POSTs signed JSON to this URL when the chosen events happen.</DialogDescription>
      </DialogHeader>
      <form id="webhook-form" class="grid gap-4" @submit.prevent="save">
        <div class="grid gap-2">
          <Label for="wh-url" :class="labelClass">URL</Label>
          <Input id="wh-url" v-model="form.url" type="url" class="font-mono" required spellcheck="false" placeholder="https://hooks.example.com/afsc" :aria-invalid="!!errors.url" />
          <FormError :message="errors.url" />
        </div>
        <div class="grid gap-2">
          <Label for="wh-desc" :class="labelClass">Description</Label>
          <Input id="wh-desc" v-model="form.description" maxlength="200" :aria-invalid="!!errors.description" />
          <FormError :message="errors.description" />
        </div>
        <div class="grid gap-2" role="group" aria-labelledby="wh-events">
          <div class="flex items-center justify-between gap-2">
            <span id="wh-events" :class="labelClass">Events</span>
            <label class="flex items-center gap-2 text-xs">
              <Checkbox :model-value="allSelected" @update:model-value="toggleAll($event === true)" />
              All
            </label>
          </div>
          <div class="grid gap-2 rounded-md border p-3">
            <label v-for="ev in events" :key="ev.name" class="flex items-start gap-2.5">
              <Checkbox class="mt-0.5" :model-value="form.events.includes(ev.name)" @update:model-value="toggle(ev.name, $event === true)" />
              <span class="grid gap-0.5">
                <span class="font-mono text-xs">{{ ev.name }}</span>
                <span class="text-xs text-muted-foreground">{{ ev.description }}</span>
              </span>
            </label>
          </div>
          <FormError :message="errors.events" />
        </div>
        <div class="flex items-center gap-2">
          <Switch id="wh-enabled" v-model="form.enabled" />
          <Label for="wh-enabled">Enabled</Label>
        </div>
        <FormError :message="errors.form" />
      </form>
      <DialogFooter>
        <Button variant="outline" @click="open = false">
          Cancel
        </Button>
        <Button type="submit" form="webhook-form" :disabled="saving || !form.url || !form.events.length">
          {{ saving ? 'Saving' : endpoint ? 'Save' : 'Create endpoint' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
