<script setup lang="ts">
import type { FieldDesc, FieldKind } from '@/components/settings/groups'
import type { SettingsResponse } from '@/types/api'
import { TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { settingsGroups, useCspReportOnly } from '@/components/settings/groups'
import SettingsField from '@/components/settings/SettingsField.vue'
import { API } from '@/composables/useAuth'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'settings.manage' })

type Values = Record<string, unknown>

const route = useRoute()
const { authFetch } = useAuth()
const { reportOnly } = useCspReportOnly()

const desc = computed(() => settingsGroups.find(g => g.slug === String(route.params.group)))
useHead(() => ({ title: `${desc.value?.title ?? 'Settings'} · Settings · AFSC` }))

const data = ref<SettingsResponse | null>(null)
const loadError = ref('')
const form = ref<Values>({})
const saved = ref('')
const errors = ref<{ field: string, message: string } | null>(null)
const saving = ref(false)

const themeName = computed(() => data.value?.theme.name ?? '')
const isTheme = computed(() => desc.value?.group === 'theme_settings')

const themeKinds: Record<string, FieldKind> = { bool: 'switch', number: 'number', select: 'select', color: 'color', string: 'text' }
const fields = computed<FieldDesc[]>(() => {
  if (!isTheme.value)
    return desc.value?.fields ?? []
  return (data.value?.theme.settings ?? []).map(d => ({ key: d.key, label: d.label || d.key, kind: themeKinds[d.type] ?? 'text', options: d.options }))
})

function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v ?? {}))
}

function reset() {
  errors.value = null
  const d = desc.value
  if (!d || !data.value)
    return
  if (isTheme.value) {
    const all = (data.value.values.theme_settings ?? {}) as Record<string, Values>
    const cur = all[themeName.value] ?? {}
    form.value = Object.fromEntries(data.value.theme.settings.map(s => [s.key, s.key in cur ? cur[s.key] : s.default]))
  }
  else {
    form.value = clone(data.value.values[d.group] as Values)
  }
  saved.value = JSON.stringify(form.value)
}

async function load() {
  loadError.value = ''
  try {
    data.value = await authFetch<SettingsResponse>(`${API}/settings`)
    reset()
  }
  catch (e) {
    loadError.value = errorData(e).message ?? 'Could not load settings.'
  }
}
load()
watch(desc, reset)

const dirty = computed(() => JSON.stringify(form.value) !== saved.value)

function errorFor(key: string) {
  const e = errors.value
  if (!e)
    return ''
  return e.field === key || e.field.startsWith(`${key}.`) ? e.message : ''
}

async function save() {
  const d = desc.value
  if (!d || !data.value)
    return
  saving.value = true
  errors.value = null
  const body = isTheme.value
    ? { ...(data.value.values.theme_settings as Record<string, Values>), [themeName.value]: form.value }
    : form.value
  try {
    const res = await authFetch<{ group: string, value: unknown }>(`${API}/settings/${d.group}`, { method: 'PUT', body })
    data.value.values[d.group] = res.value
    if (d.group === 'security')
      reportOnly.value = !!(res.value as { csp_report_only?: boolean }).csp_report_only
    reset()
    toast.success(`${d.title} settings saved. The page cache was cleared.`)
  }
  catch (e) {
    const { message, field } = errorData(e)
    let f = field ?? ''
    if (isTheme.value && f.startsWith(`${themeName.value}.`))
      f = f.slice(themeName.value.length + 1)
    errors.value = { field: f, message: message ?? 'Could not save.' }
  }
  finally {
    saving.value = false
  }
}

const unmatched = computed(() => {
  const e = errors.value
  return e && !fields.value.some(f => errorFor(f.key)) ? e.message : ''
})
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Settings" :description="desc?.description" />

    <div class="flex flex-col gap-6 md:flex-row md:items-start">
      <nav aria-label="Settings sections" class="flex flex-wrap gap-1 md:w-44 md:shrink-0 md:flex-col">
        <NuxtLink
          v-for="g in settingsGroups"
          :key="g.slug"
          :to="`/settings/${g.slug}`"
          class="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          active-class="bg-muted font-medium text-foreground"
        >
          {{ g.title }}
        </NuxtLink>
      </nav>

      <div class="min-w-0 max-w-2xl flex-1">
        <p v-if="!desc" class="text-sm text-muted-foreground">
          There's no such settings section. Pick one from the list.
        </p>
        <FormError v-else-if="loadError" :message="loadError" />
        <div v-else-if="!data" class="grid gap-4">
          <Skeleton v-for="i in 4" :key="i" class="h-14 w-full" />
        </div>

        <form v-else class="grid gap-5" @submit.prevent="save">
          <h2 class="font-display text-sm tracking-wide uppercase">
            {{ desc.title }}
            <span v-if="isTheme" class="ml-2 font-mono text-[11px] tracking-widest text-muted-foreground normal-case">{{ themeName }}</span>
          </h2>

          <Alert v-if="desc.group === 'snippets'" variant="destructive">
            <TriangleAlert />
            <AlertTitle>Unsanitized HTML</AlertTitle>
            <AlertDescription>
              <p>
                Snippets are inserted into every public page exactly as written. Only admins can edit them, and a
                mistake here can break or compromise the whole site. Audit every change before saving.
              </p>
            </AlertDescription>
          </Alert>

          <p v-if="isTheme && !fields.length" class="text-sm text-muted-foreground">
            The active theme declares no settings.
          </p>

          <SettingsField
            v-for="f in fields"
            :key="f.key"
            v-model="form[f.key]"
            :field="f"
            :error="errorFor(f.key)"
            :error-field="errors?.field"
          />

          <FormError :message="unmatched" />
          <div v-if="fields.length" class="flex flex-wrap gap-2">
            <Button type="submit" size="sm" :disabled="saving || !dirty">
              {{ saving ? 'Saving…' : 'Save' }}
            </Button>
            <Button type="button" size="sm" variant="ghost" :disabled="saving || !dirty" @click="reset">
              Discard changes
            </Button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>
