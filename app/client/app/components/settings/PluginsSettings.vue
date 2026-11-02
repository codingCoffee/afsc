<script setup lang="ts">
import type { FieldDesc, FieldKind } from './groups'
import type { PluginListing, PluginsValue } from '@/types/api'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { API, errorData } from '@/composables/useAuth'
import SettingsField from './SettingsField.vue'

const props = defineProps<{ plugins: PluginListing[], value: PluginsValue | undefined }>()
const emit = defineEmits<{ saved: [value: PluginsValue] }>()

const { authFetch } = useAuth()

const kinds: Record<string, FieldKind> = { bool: 'switch', number: 'number', select: 'select', color: 'color', string: 'text' }

function fieldsOf(p: PluginListing): FieldDesc[] {
  return p.settings.map(d => ({ key: d.key, label: d.label || d.key, kind: kinds[d.type] ?? 'text', options: d.options, help: d.help, min: d.min, max: d.max }))
}

const enabled = ref<Record<string, boolean>>({})
const values = ref<Record<string, Record<string, unknown>>>({})
const saved = ref('')
const error = ref<{ field: string, message: string } | null>(null)
const saving = ref(false)

function reset() {
  error.value = null
  const v = props.value
  enabled.value = Object.fromEntries(props.plugins.map(p => [p.id, p.always_on || (v?.enabled?.[p.id] ?? p.default_enabled)]))
  values.value = Object.fromEntries(props.plugins.map((p) => {
    const cur = v?.settings?.[p.id] ?? {}
    return [p.id, Object.fromEntries(p.settings.map(d => [d.key, d.key in cur ? cur[d.key] : d.default]))]
  }))
  saved.value = JSON.stringify([enabled.value, values.value])
}
watch(() => [props.plugins, props.value], reset, { immediate: true })

const dirty = computed(() => JSON.stringify([enabled.value, values.value]) !== saved.value)

function errorFor(id: string, key: string) {
  const e = error.value
  return e && e.field === `settings.${id}.${key}` ? e.message : ''
}

const unmatched = computed(() => {
  const e = error.value
  if (!e)
    return ''
  return e.field.startsWith('settings.') && props.plugins.some(p => p.settings.some(d => e.field === `settings.${p.id}.${d.key}`)) ? '' : e.message
})

async function save() {
  saving.value = true
  error.value = null
  // values for plugins no longer compiled in are kept as they are
  const body: PluginsValue = {
    enabled: { ...(props.value?.enabled ?? {}), ...enabled.value },
    settings: { ...(props.value?.settings ?? {}), ...values.value },
  }
  try {
    const res = await authFetch<{ group: string, value: PluginsValue }>(`${API}/settings/plugins`, { method: 'PUT', body })
    emit('saved', res.value)
    toast.success('Plugin settings saved. The page cache was cleared.')
  }
  catch (e) {
    const { message, field } = errorData(e)
    error.value = { field: field ?? '', message: message ?? 'Could not save.' }
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <form class="grid gap-4" @submit.prevent="save">
    <p v-if="!plugins.length" class="text-sm text-muted-foreground">
      No plugins are compiled in.
    </p>
    <Card v-for="p in plugins" :key="p.id" class="gap-4 py-4">
      <CardHeader class="px-4">
        <div class="flex items-start justify-between gap-4">
          <div class="grid gap-1">
            <CardTitle class="flex flex-wrap items-center gap-2 text-sm">
              <Label :for="`plg-${p.id}`">{{ p.name }}</Label>
              <span class="font-mono text-[11px] text-muted-foreground">{{ p.id }}</span>
              <Badge v-if="p.always_on" variant="secondary">
                Always on
              </Badge>
            </CardTitle>
            <CardDescription v-if="p.description" class="text-xs">
              {{ p.description }}
            </CardDescription>
          </div>
          <Switch :id="`plg-${p.id}`" v-model="enabled[p.id]" :disabled="p.always_on" />
        </div>
      </CardHeader>
      <CardContent v-if="p.settings.length && values[p.id]" class="grid gap-4 px-4">
        <SettingsField
          v-for="f in fieldsOf(p)"
          :key="f.key"
          v-model="values[p.id]![f.key]"
          :field="{ ...f, key: `${p.id}-${f.key}` }"
          :error="errorFor(p.id, f.key)"
        />
      </CardContent>
    </Card>

    <FormError :message="unmatched" />
    <div v-if="plugins.length" class="flex flex-wrap gap-2">
      <Button type="submit" size="sm" :disabled="saving || !dirty">
        {{ saving ? 'Saving…' : 'Save' }}
      </Button>
      <Button type="button" size="sm" variant="ghost" :disabled="saving || !dirty" @click="reset">
        Discard changes
      </Button>
    </div>
  </form>
</template>
