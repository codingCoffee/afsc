<script setup lang="ts">
import type { BlocklistKind, SpamBlocklist } from '@/types/api'
import { computed, ref } from 'vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { API, errorData } from '@/composables/useAuth'

const KINDS: { kind: BlocklistKind, label: string, help: string, placeholder: string }[] = [
  { kind: 'keyword', label: 'Keywords', help: 'Matched anywhere in the comment, name or website, ignoring case.', placeholder: 'casino' },
  { kind: 'domain', label: 'Domains', help: 'Links, website and email domains. Subdomains match too.', placeholder: 'spam.example' },
  { kind: 'ip', label: 'IP addresses', help: 'Exact IPv4 or IPv6 addresses.', placeholder: '192.0.2.10' },
  { kind: 'cidr', label: 'IP ranges', help: 'CIDR ranges.', placeholder: '198.51.100.0/24' },
  { kind: 'email', label: 'Emails', help: 'Exact addresses, ignoring case.', placeholder: 'someone@example.com' },
]

const { authFetch } = useAuth()
const data = ref<SpamBlocklist | null>(null)
const text = ref<Record<BlocklistKind, string>>({ keyword: '', domain: '', ip: '', cidr: '', email: '' })
const saved = ref('')
const error = ref<{ field: string, message: string } | null>(null)
const saving = ref(false)

function fill(d: SpamBlocklist) {
  data.value = d
  for (const k of KINDS)
    text.value[k.kind] = (d.entries[k.kind] ?? []).join('\n')
  saved.value = JSON.stringify(text.value)
}

async function load() {
  try {
    fill(await authFetch<SpamBlocklist>(`${API}/spam/blocklist`))
  }
  catch (e) {
    error.value = { field: '', message: errorData(e).message ?? 'Could not load the blocklist.' }
  }
}
load()

const dirty = computed(() => JSON.stringify(text.value) !== saved.value)

function lines(s: string) {
  return s.split('\n').map(l => l.trim()).filter(Boolean)
}

async function save() {
  saving.value = true
  error.value = null
  const entries = Object.fromEntries(KINDS.map(k => [k.kind, lines(text.value[k.kind])]))
  try {
    fill(await authFetch<SpamBlocklist>(`${API}/spam/blocklist`, { method: 'PUT', body: { entries } }))
    toast.success('Blocklist saved')
  }
  catch (e) {
    const { message, field } = errorData(e)
    error.value = { field: field ?? '', message: message ?? 'Could not save the blocklist.' }
  }
  finally {
    saving.value = false
  }
}

async function retrain() {
  try {
    await authFetch(`${API}/spam/retrain`, { method: 'POST' })
    toast.success('Retraining queued. It runs in the background.')
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not queue retraining.')
  }
}

function errorFor(kind: BlocklistKind) {
  const e = error.value
  return e && e.field.startsWith(`entries.${kind}`) ? e.message : ''
}
</script>

<template>
  <section class="grid gap-5 border-t pt-6" aria-labelledby="spam-heading">
    <div class="grid gap-1">
      <h2 id="spam-heading" class="font-display text-sm tracking-wide uppercase">
        Spam filter
      </h2>
      <p v-if="data" class="text-sm text-muted-foreground">
        <template v-if="data.classifier.active">
          The classifier is active, trained on {{ data.classifier.spam }} spam and {{ data.classifier.ham }} approved comments.
        </template>
        <template v-else>
          The classifier starts scoring once it has {{ data.classifier.min }} spam and {{ data.classifier.min }} approved comments
          (now {{ data.classifier.spam }} and {{ data.classifier.ham }}). Until then the other checks decide.
        </template>
        It retrains nightly.
        <Button variant="link" size="sm" class="h-auto p-0" @click="retrain">
          Retrain now
        </Button>
      </p>
    </div>

    <form class="grid gap-5" @submit.prevent="save">
      <p class="text-xs text-muted-foreground">
        One entry per line. Each match adds 5 to a comment's spam score.
      </p>
      <div v-for="k in KINDS" :key="k.kind" class="grid gap-1.5">
        <Label :for="`block-${k.kind}`">{{ k.label }}</Label>
        <Textarea
          :id="`block-${k.kind}`"
          v-model="text[k.kind]"
          rows="3"
          :placeholder="k.placeholder"
          class="font-mono text-xs"
          :aria-invalid="!!errorFor(k.kind)"
        />
        <p class="text-xs text-muted-foreground">
          {{ k.help }}
        </p>
        <FormError :message="errorFor(k.kind)" />
      </div>
      <FormError :message="error && !KINDS.some(k => errorFor(k.kind)) ? error.message : ''" />
      <div class="flex flex-wrap gap-2">
        <Button type="submit" size="sm" :disabled="saving || !dirty">
          {{ saving ? 'Saving…' : 'Save blocklist' }}
        </Button>
        <Button type="button" size="sm" variant="ghost" :disabled="saving || !dirty" @click="data && fill(data)">
          Discard changes
        </Button>
      </div>
    </form>
  </section>
</template>
