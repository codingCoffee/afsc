<script setup lang="ts">
import type { Term } from '@/types/api'
import { ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import TermPicker from '@/components/taxonomy/TermPicker.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

const props = defineProps<{ source: Term | null }>()
const emit = defineEmits<{ merged: [into: Term] }>()
const open = defineModel<boolean>('open', { default: false })

const { authFetch } = useAuth()
const target = ref<Term | null>(null)
const saving = ref(false)
const error = ref('')

watch(open, (v) => {
  if (v) {
    target.value = null
    error.value = ''
  }
})

async function merge() {
  if (!props.source || !target.value)
    return
  saving.value = true
  error.value = ''
  try {
    const into = await authFetch<Term>(`/admin/api/v1/terms/${props.source.id}/merge`, { method: 'POST', body: { into: target.value.id } })
    toast.success(`Merged into ${into.name}.`)
    emit('merged', into)
    open.value = false
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not merge.'
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
        <DialogTitle>Merge {{ source?.name }}</DialogTitle>
        <DialogDescription>
          Its {{ source?.count ?? 0 }} posts move to the term you pick, then it is deleted and its archive redirects there.
        </DialogDescription>
      </DialogHeader>
      <p v-if="target" class="flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
        <span>Into <strong>{{ target.name }}</strong></span>
        <Button variant="ghost" size="xs" @click="target = null">
          Change
        </Button>
      </p>
      <TermPicker v-else-if="source" :taxonomy="source.taxonomy" :exclude="[source.id]" placeholder="Search the term to merge into" @pick="t => (target = t)" />
      <FormError :message="error" />
      <DialogFooter>
        <Button variant="destructive" :disabled="!target || saving" @click="merge">
          {{ saving ? 'Merging' : 'Merge' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
