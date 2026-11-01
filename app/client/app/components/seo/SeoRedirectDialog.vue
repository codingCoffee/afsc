<script setup lang="ts">
import type { Redirect, RedirectConflict, RedirectInput, RedirectMatch, RedirectSaved, RedirectTestResult } from '@/types/api'
import { ArrowRight, FlaskConical } from '@lucide/vue'
import { computed, reactive, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { API, errorData } from '@/composables/useAuth'

const props = defineProps<{ redirect?: Redirect | null, source?: string }>()
const emit = defineEmits<{ saved: [RedirectSaved] }>()
const open = defineModel<boolean>('open', { default: false })

const { authFetch } = useAuth()
const statuses = [301, 302, 307, 308, 410, 451]
const matches: RedirectMatch[] = ['exact', 'prefix', 'regex']
const form = reactive<RedirectInput>({ source: '', match: 'exact', target: '', status: 301, note: '' })
const errors = ref<Record<string, string>>({})
const conflict = ref<RedirectConflict | null>(null)
const saving = ref(false)
const gone = computed(() => form.status === 410 || form.status === 451)

const testPaths = ref('')
const results = ref<RedirectTestResult[]>([])
const testError = ref('')

watch(open, (o) => {
  if (!o)
    return
  const r = props.redirect
  Object.assign(form, r
    ? { source: r.source, match: r.match, target: r.target ?? '', status: r.status, note: r.note }
    : { source: props.source ?? '', match: 'exact', target: '', status: 301, note: '' })
  errors.value = {}
  conflict.value = null
  results.value = []
  testError.value = ''
  testPaths.value = props.source ?? ''
})

async function save(flatten = false) {
  saving.value = true
  errors.value = {}
  const body = { ...form, target: gone.value ? null : form.target, status: Number(form.status) }
  const url = props.redirect ? `${API}/redirects/${props.redirect.id}` : `${API}/redirects`
  try {
    const res = await authFetch<RedirectSaved>(url, { method: props.redirect ? 'PUT' : 'POST', body, query: flatten ? { flatten: 1 } : undefined })
    conflict.value = null
    open.value = false
    for (const w of res.warnings)
      toast.warning(w)
    if (res.flattened_ids.length)
      toast.info(`${res.flattened_ids.length} redirects that pointed here now go straight to the target.`)
    emit('saved', res)
  }
  catch (e) {
    const data = errorData<RedirectConflict>(e)
    if (data.hops) {
      conflict.value = data as RedirectConflict
      return
    }
    errors.value = { [data.field ?? 'form']: data.message ?? 'Could not save the redirect.' }
  }
  finally {
    saving.value = false
  }
}

async function runTest() {
  testError.value = ''
  try {
    const paths = testPaths.value.split('\n').map(p => p.trim()).filter(Boolean)
    results.value = (await authFetch<{ results: RedirectTestResult[] }>(`${API}/redirects/test`, {
      method: 'POST',
      body: { match: form.match, source: form.source, target: gone.value ? '' : form.target, paths },
    })).results
  }
  catch (e) {
    testError.value = errorData(e).message ?? 'Could not run the test.'
  }
}

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ conflict ? 'The target redirects again' : redirect ? 'Edit redirect' : 'New redirect' }}</DialogTitle>
        <DialogDescription>
          {{ conflict ? conflict.message : 'Sources are paths. Targets are paths or full URLs.' }}
        </DialogDescription>
      </DialogHeader>

      <div v-if="conflict" class="grid gap-3">
        <ol class="grid gap-1 font-mono text-xs">
          <li v-for="(h, i) in conflict.hops" :key="i" class="flex flex-wrap items-center gap-1.5 break-all">
            <span>{{ h.from }}</span>
            <ArrowRight class="size-3 shrink-0" />
            <span>{{ h.to ?? '—' }}</span>
            <span class="text-muted-foreground">({{ h.status }}, {{ h.via }})</span>
          </li>
        </ol>
        <p v-if="conflict.code === 'chain' && conflict.flattened_target" class="text-sm">
          Point the redirect straight at <span class="font-mono break-all">{{ conflict.flattened_target }}</span> instead?
        </p>
        <p v-else class="text-sm text-muted-foreground">
          Change the target and try again.
        </p>
      </div>

      <form v-else id="redirect-form" class="grid gap-4" @submit.prevent="save()">
        <div class="grid gap-2">
          <Label for="r-source" :class="labelClass">Source</Label>
          <Input id="r-source" v-model="form.source" class="font-mono" required spellcheck="false" placeholder="/old-path/" :aria-invalid="!!errors.source" />
          <FormError :message="errors.source" />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div class="grid gap-2">
            <Label for="r-match" :class="labelClass">Match</Label>
            <NativeSelect id="r-match" v-model="form.match" class="w-full">
              <NativeSelectOption v-for="m in matches" :key="m" :value="m">
                {{ m }}
              </NativeSelectOption>
            </NativeSelect>
          </div>
          <div class="grid gap-2">
            <Label for="r-status" :class="labelClass">Status</Label>
            <NativeSelect id="r-status" v-model.number="form.status" class="w-full">
              <NativeSelectOption v-for="s in statuses" :key="s" :value="s">
                {{ s }}
              </NativeSelectOption>
            </NativeSelect>
          </div>
        </div>
        <FormError :message="errors.match ?? errors.status" />
        <div class="grid gap-2">
          <Label for="r-target" :class="labelClass">Target</Label>
          <Input
            id="r-target"
            :model-value="form.target ?? ''"
            class="font-mono"
            spellcheck="false"
            :disabled="gone"
            :placeholder="gone ? 'No target: the path is gone' : '/new-path/ or https://…'"
            :aria-invalid="!!errors.target"
            @update:model-value="form.target = String($event)"
          />
          <FormError :message="errors.target" />
        </div>
        <div class="grid gap-2">
          <Label for="r-note" :class="labelClass">Note</Label>
          <Input id="r-note" v-model="form.note" />
        </div>
        <FormError :message="errors.form" />

        <Collapsible>
          <CollapsibleTrigger as-child>
            <Button type="button" variant="ghost" size="sm" class="justify-self-start">
              <FlaskConical />
              Test the rule
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent class="grid gap-2 pt-2">
            <Textarea v-model="testPaths" rows="3" class="font-mono text-xs" placeholder="One path per line" aria-label="Paths to test" />
            <Button type="button" variant="outline" size="sm" class="justify-self-start" :disabled="!form.source || !testPaths.trim()" @click="runTest">
              Run test
            </Button>
            <FormError :message="testError" />
            <ul v-if="results.length" class="grid gap-1 font-mono text-xs">
              <li v-for="r in results" :key="r.path" class="flex flex-wrap items-center gap-1.5 break-all">
                <span :class="r.matched ? '' : 'text-muted-foreground'">{{ r.path }}</span>
                <template v-if="r.matched">
                  <ArrowRight class="size-3 shrink-0" />
                  <span>{{ r.target || form.status }}</span>
                </template>
                <span v-else class="text-muted-foreground">no match</span>
              </li>
            </ul>
          </CollapsibleContent>
        </Collapsible>
      </form>

      <DialogFooter>
        <template v-if="conflict">
          <Button variant="outline" @click="conflict = null">
            Back
          </Button>
          <Button v-if="conflict.code === 'chain' && conflict.flattened_target" :disabled="saving" @click="save(true)">
            Save flattened
          </Button>
        </template>
        <Button v-else type="submit" form="redirect-form" :disabled="saving || !form.source">
          {{ saving ? 'Saving' : 'Save redirect' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
