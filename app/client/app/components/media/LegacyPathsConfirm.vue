<script setup lang="ts">
import type { MediaDetail } from '@/types/api'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { buttonVariants } from '@/components/ui/button'

defineProps<{ title: string, description: string, action: string, paths: MediaDetail['legacy_paths'] }>()
const emit = defineEmits<{ confirm: [] }>()
const open = defineModel<boolean>('open', { default: false })
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })
</script>

<template>
  <AlertDialog v-model:open="open">
    <AlertDialogContent class="sm:max-w-lg">
      <AlertDialogHeader>
        <AlertDialogTitle class="font-display text-base tracking-wide uppercase">
          {{ title }}
        </AlertDialogTitle>
        <AlertDialogDescription>
          {{ description }}
        </AlertDialogDescription>
      </AlertDialogHeader>
      <ul v-if="paths.length" class="max-h-56 divide-y overflow-y-auto rounded-md border">
        <li v-for="p in paths" :key="p.path" class="flex items-center gap-3 px-3 py-2">
          <span class="min-w-0 flex-1 truncate font-mono text-xs" :title="p.path">{{ p.path }}</span>
          <span class="shrink-0 font-mono text-[11px] text-muted-foreground">
            {{ p.hits }} hits{{ p.last_hit_at ? ` · ${fmt.format(new Date(p.last_hit_at))}` : '' }}
          </span>
        </li>
      </ul>
      <AlertDialogFooter>
        <AlertDialogCancel class="font-mono text-xs tracking-widest uppercase">
          Cancel
        </AlertDialogCancel>
        <AlertDialogAction
          :class="buttonVariants({ variant: 'destructive' })"
          class="font-mono text-xs tracking-widest uppercase"
          @click="emit('confirm')"
        >
          {{ action }}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
