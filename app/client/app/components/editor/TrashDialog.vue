<script setup lang="ts">
import { ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'

export interface TrashChoice { gone: '410' | '301' | 'none', target?: string }

// published asks what the old URL should answer; for anything else trashing needs no choice
const props = defineProps<{ count?: number, published: boolean }>()
const emit = defineEmits<{ confirm: [choice: TrashChoice] }>()
const open = defineModel<boolean>('open', { default: false })

const gone = ref<TrashChoice['gone']>('410')
const target = ref('')
watch(open, (v) => {
  if (v) {
    gone.value = '410'
    target.value = ''
  }
})

function confirm() {
  emit('confirm', gone.value === '301' ? { gone: '301', target: target.value.trim() } : { gone: gone.value })
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ props.count && props.count > 1 ? `Move ${props.count} items to trash?` : 'Move to trash?' }}</DialogTitle>
        <DialogDescription>
          {{ published ? 'Choose what visitors and search engines get at the old URL. Restoring later is still possible.' : 'You can restore it from the trash later.' }}
        </DialogDescription>
      </DialogHeader>
      <RadioGroup v-if="published" v-model="gone" class="grid gap-3">
        <Label class="flex items-start gap-3 font-normal">
          <RadioGroupItem value="410" class="mt-0.5" />
          <span><span class="font-medium">410 Gone</span><span class="block text-xs text-muted-foreground">Tells search engines it was removed on purpose.</span></span>
        </Label>
        <Label class="flex items-start gap-3 font-normal">
          <RadioGroupItem value="301" class="mt-0.5" />
          <span><span class="font-medium">301 redirect</span><span class="block text-xs text-muted-foreground">Sends visitors to another URL.</span></span>
        </Label>
        <Input v-if="gone === '301'" v-model="target" placeholder="/new-url/ or https://…" aria-label="Redirect target" />
        <Label class="flex items-start gap-3 font-normal">
          <RadioGroupItem value="none" class="mt-0.5" />
          <span><span class="font-medium">Nothing (404)</span><span class="block text-xs text-muted-foreground">The URL simply stops existing.</span></span>
        </Label>
      </RadioGroup>
      <DialogFooter>
        <Button variant="outline" @click="open = false">
          Cancel
        </Button>
        <Button variant="destructive" :disabled="published && gone === '301' && !target.trim()" @click="confirm">
          Move to trash
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
