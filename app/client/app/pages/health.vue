<script setup lang="ts">
import { RotateCw } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { API } from '~/composables/useAuth'

interface Health {
  all: boolean
  db: boolean
  api: boolean
}

definePageMeta({ layout: 'auth' })
useHead({ title: 'Status · AFSC' })

const down: Health = { all: false, db: false, api: false }

const health = ref<Health | null>(null)
const loading = ref(false)
const httpStatus = ref<number | null>(null)
const latency = ref<number | null>(null)
const checkedAt = ref<Date | null>(null)

const panels = [
  { key: 'all', code: 'SYS/00', label: 'All systems' },
  { key: 'db', code: 'DB/01', label: 'Postgres' },
  { key: 'api', code: 'API/02', label: 'API' },
] as const

const summary = computed(() => {
  if (!health.value)
    return 'Taking a reading…'
  if (health.value.all)
    return 'All systems nominal. Nothing to see here.'
  if (!health.value.api)
    return 'The API isn\'t answering. Is the server running?'
  return 'Postgres isn\'t answering. Check DATABASE_URL and the database.'
})

async function check() {
  loading.value = true
  const start = performance.now()
  try {
    const res = await $fetch.raw<Health>(`${API}/ht`, { ignoreResponseError: true })
    health.value = res._data ?? down
    httpStatus.value = res.status
  }
  catch {
    health.value = down
    httpStatus.value = null
  }
  latency.value = Math.round(performance.now() - start)
  checkedAt.value = new Date()
  loading.value = false
}

onMounted(check)
</script>

<template>
  <section class="bg-grid border-b">
    <div class="mx-auto max-w-7xl px-4 pt-12 pb-20 sm:px-8 sm:pt-20 sm:pb-28">
      <div class="flex flex-col gap-8 sm:flex-row sm:items-end sm:justify-between">
        <div class="animate-rise">
          <p class="font-mono text-[11px] tracking-widest text-primary uppercase">
            § status — {{ API }}/ht
          </p>
          <h1 class="mt-4 font-display text-4xl tracking-tight uppercase sm:text-6xl">
            Pulse.
          </h1>
          <p class="mt-4 max-w-md text-muted-foreground" aria-live="polite">
            {{ summary }}
          </p>
        </div>

        <Button
          variant="outline"
          class="self-start font-mono text-xs tracking-widest uppercase sm:self-auto"
          :disabled="loading"
          @click="check"
        >
          <RotateCw :class="{ 'animate-spin': loading }" />
          {{ loading ? 'Reading' : 'Re-check' }}
        </Button>
      </div>

      <div class="mt-12 grid gap-4 sm:grid-cols-3">
        <Card
          v-for="(panel, i) in panels"
          :key="panel.key"
          class="animate-rise gap-0 overflow-hidden py-0 shadow-none"
          :style="{ animationDelay: `${120 + i * 100}ms` }"
        >
          <CardHeader class="flex flex-row items-center justify-between border-b px-5 py-3 font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
            <span>{{ panel.code }}</span>
            <span class="relative flex size-2">
              <template v-if="health">
                <span
                  class="absolute inline-flex size-full animate-ping rounded-full opacity-60"
                  :class="health[panel.key] ? 'bg-ok' : 'bg-destructive'"
                />
                <span
                  class="relative inline-flex size-2 rounded-full"
                  :class="health[panel.key] ? 'bg-ok' : 'bg-destructive'"
                />
              </template>
              <span v-else class="relative inline-flex size-2 animate-pulse rounded-full bg-muted-foreground" />
            </span>
          </CardHeader>
          <CardContent class="px-5 pt-8 pb-6">
            <p class="font-mono text-[11px] tracking-widest text-muted-foreground uppercase">
              {{ panel.label }}
            </p>
            <p
              class="mt-2 font-display text-4xl tracking-tight sm:text-5xl"
              :class="!health ? 'text-muted-foreground animate-pulse' : health[panel.key] ? 'text-ok' : 'text-destructive'"
            >
              {{ !health ? '···' : health[panel.key] ? 'OK' : 'FAIL' }}
            </p>
          </CardContent>
        </Card>
      </div>

      <div class="mt-6 flex flex-wrap gap-2">
        <Badge variant="outline" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
          http {{ httpStatus ?? '—' }}
        </Badge>
        <Badge variant="outline" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
          {{ latency ?? '—' }} ms
        </Badge>
        <Badge variant="outline" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
          checked {{ checkedAt ? checkedAt.toLocaleTimeString() : '—' }}
        </Badge>
      </div>
    </div>
  </section>
</template>
