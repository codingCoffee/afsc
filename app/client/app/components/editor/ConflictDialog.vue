<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

export interface Conflict { current_version: number, modified_by: string | null, modified_at: string | null }

defineProps<{ conflict: Conflict | null, canOverwrite: boolean }>()
const emit = defineEmits<{ reload: [], overwrite: [] }>()
const open = defineModel<boolean>('open', { default: false })
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>Someone else saved this post</DialogTitle>
        <DialogDescription>
          {{ conflict?.modified_by ?? 'Another user' }} saved a newer version{{ conflict?.modified_at ? ` on ${fmt.format(new Date(conflict.modified_at))}` : '' }}.
          Reload to get their version (your unsaved changes are lost){{ canOverwrite ? ', or overwrite it with yours' : '' }}.
        </DialogDescription>
      </DialogHeader>
      <DialogFooter class="gap-2">
        <Button variant="outline" @click="emit('reload')">
          Reload
        </Button>
        <Button v-if="canOverwrite" variant="destructive" @click="emit('overwrite')">
          Overwrite
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
