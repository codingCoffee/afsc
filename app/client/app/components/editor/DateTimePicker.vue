<script setup lang="ts">
import type { DateValue } from '@internationalized/date'
import { CalendarDate } from '@internationalized/date'
import { CalendarClock } from '@lucide/vue'
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

const value = defineModel<string | null>({ required: true })
defineProps<{ emptyLabel?: string }>()
const open = ref(false)
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const pad = (n: number) => String(n).padStart(2, '0')

const date = computed(() => value.value ? new Date(value.value) : null)
const day = computed(() => date.value ? new CalendarDate(date.value.getFullYear(), date.value.getMonth() + 1, date.value.getDate()) : undefined)
const time = computed(() => date.value ? `${pad(date.value.getHours())}:${pad(date.value.getMinutes())}` : '09:00')

function set(d: { year: number, month: number, day: number } | undefined, t: string) {
  if (!d)
    return
  const [h, m] = t.split(':').map(Number)
  value.value = new Date(d.year, d.month - 1, d.day, h || 0, m || 0).toISOString()
}

function onDay(d: DateValue | undefined) {
  set(d, time.value)
}

function onTime(t: string | number) {
  const now = new Date()
  set(day.value ?? { year: now.getFullYear(), month: now.getMonth() + 1, day: now.getDate() }, String(t))
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button variant="outline" size="sm" class="w-full justify-start font-normal">
        <CalendarClock />
        {{ date ? fmt.format(date) : (emptyLabel ?? 'Immediately') }}
      </Button>
    </PopoverTrigger>
    <PopoverContent align="start" class="w-auto p-0">
      <Calendar :model-value="day" initial-focus @update:model-value="onDay" />
      <div class="flex items-center gap-2 border-t p-3">
        <Input type="time" :model-value="time" class="h-8 w-32" aria-label="Time" @update:model-value="onTime" />
        <Button variant="ghost" size="sm" @click="value = new Date().toISOString()">
          Now
        </Button>
        <Button variant="ghost" size="sm" class="ml-auto" @click="value = null; open = false">
          Clear
        </Button>
      </div>
    </PopoverContent>
  </Popover>
</template>
