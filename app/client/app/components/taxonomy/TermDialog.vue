<script setup lang="ts">
import type { Term, TermInput } from '@/types/api'
import { computed, reactive, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

// term is null when creating; categories lists parent options (categories only)
const props = defineProps<{ taxonomy: 'category' | 'tag', term: Term | null, categories?: { term: Term, depth: number }[] }>()
const emit = defineEmits<{ saved: [term: Term] }>()
const open = defineModel<boolean>('open', { default: false })

const { authFetch } = useAuth()
const form = reactive<TermInput>({ taxonomy: 'tag', name: '', slug: '', description: '', parent_id: null })
const parent = ref('')
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const noun = computed(() => (props.taxonomy === 'category' ? 'category' : 'tag'))

watch(open, (v) => {
  if (!v)
    return
  const t = props.term
  Object.assign(form, { taxonomy: props.taxonomy, name: t?.name ?? '', slug: t?.slug ?? '', description: t?.description ?? '', parent_id: t?.parent_id ?? null })
  parent.value = t?.parent_id ? String(t.parent_id) : ''
  errors.value = {}
})

// a category can't move under itself or its own subcategories
const parentOptions = computed(() => {
  const list = props.categories ?? []
  if (!props.term)
    return list
  const out: typeof list = []
  let skipDepth = -1
  for (const c of list) {
    if (skipDepth >= 0 && c.depth > skipDepth)
      continue
    skipDepth = c.term.id === props.term.id ? c.depth : -1
    if (skipDepth < 0)
      out.push(c)
  }
  return out
})

async function submit() {
  saving.value = true
  errors.value = {}
  form.parent_id = parent.value ? Number(parent.value) : null
  try {
    const url = props.term ? `/admin/api/v1/terms/${props.term.id}` : '/admin/api/v1/terms'
    const saved = await authFetch<Term>(url, { method: props.term ? 'PUT' : 'POST', body: { ...form } })
    emit('saved', saved)
    open.value = false
  }
  catch (e) {
    const { message, field } = errorData(e)
    errors.value = { [field ?? 'form']: message ?? 'Could not save.' }
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ term ? `Edit ${noun}` : `New ${noun}` }}</DialogTitle>
        <DialogDescription>Changing the slug{{ taxonomy === 'category' ? ' or parent' : '' }} redirects the old archive URL.</DialogDescription>
      </DialogHeader>
      <form id="term-form" class="grid gap-4" @submit.prevent="submit">
        <div class="grid gap-2">
          <Label for="term-name">Name</Label>
          <Input id="term-name" v-model="form.name" required :aria-invalid="!!errors.name" />
          <FormError :message="errors.name" />
        </div>
        <div class="grid gap-2">
          <Label for="term-slug">Slug <span class="text-muted-foreground">(optional)</span></Label>
          <Input id="term-slug" v-model="form.slug" autocapitalize="none" spellcheck="false" class="font-mono" :aria-invalid="!!errors.slug" />
          <FormError :message="errors.slug" />
        </div>
        <div v-if="taxonomy === 'category'" class="grid gap-2">
          <Label for="term-parent">Parent</Label>
          <NativeSelect id="term-parent" v-model="parent" class="w-full">
            <NativeSelectOption value="">
              None
            </NativeSelectOption>
            <NativeSelectOption v-for="c in parentOptions" :key="c.term.id" :value="String(c.term.id)">
              {{ '  '.repeat(c.depth) }}{{ c.term.name }}
            </NativeSelectOption>
          </NativeSelect>
          <FormError :message="errors.parent_id" />
        </div>
        <div class="grid gap-2">
          <Label for="term-desc">Description</Label>
          <Textarea id="term-desc" v-model="form.description" rows="3" />
          <FormError :message="errors.description" />
        </div>
        <FormError :message="errors.form ?? errors.taxonomy" />
      </form>
      <DialogFooter>
        <Button type="submit" form="term-form" :disabled="saving || !form.name.trim()">
          {{ saving ? 'Saving' : 'Save' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
