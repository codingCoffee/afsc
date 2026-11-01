<script setup lang="ts">
import { LogOut } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

export interface SessionRow {
  session_id: string
  user_agent: string
  ip: string
  started_at: string
  last_seen: string
  expires_at: string
  current: boolean
}

defineProps<{ sessions: SessionRow[], revocable?: boolean }>()
const emit = defineEmits<{ revoke: [id: string] }>()
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <ul class="divide-y rounded-md border">
    <li v-for="s in sessions" :key="s.session_id" class="flex items-start gap-3 p-3">
      <div class="min-w-0 flex-1">
        <p class="truncate text-sm" :title="s.user_agent">
          {{ s.user_agent || 'Unknown client' }}
        </p>
        <p class="mt-1 font-mono text-[11px] text-muted-foreground">
          {{ s.ip || '—' }} · started {{ fmt.format(new Date(s.started_at)) }} · active {{ fmt.format(new Date(s.last_seen)) }}
        </p>
      </div>
      <Badge v-if="s.current" variant="outline" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
        this one
      </Badge>
      <Button v-else-if="revocable" variant="ghost" size="icon-sm" aria-label="Sign this session out" @click="emit('revoke', s.session_id)">
        <LogOut />
      </Button>
    </li>
    <li v-if="!sessions.length" class="p-3 text-sm text-muted-foreground">
      No active sessions.
    </li>
  </ul>
</template>
