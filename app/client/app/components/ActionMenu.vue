<script setup lang="ts">
import type { Component } from 'vue'
import { Ellipsis } from '@lucide/vue'
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Command, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

export interface Action {
  label: string
  icon?: Component
  danger?: boolean
  disabled?: boolean
  run: () => void
}

defineProps<{ actions: Action[], label: string }>()
const open = ref(false)

function pick(a: Action) {
  open.value = false
  a.run()
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button variant="ghost" size="icon-sm" :aria-label="label">
        <Ellipsis />
      </Button>
    </PopoverTrigger>
    <PopoverContent align="end" class="w-52 p-0">
      <Command>
        <CommandList>
          <CommandItem
            v-for="a in actions"
            :key="a.label"
            :value="a.label"
            :disabled="a.disabled"
            :class="a.danger ? 'text-destructive data-[highlighted]:text-destructive' : ''"
            @select="pick(a)"
          >
            <component :is="a.icon" v-if="a.icon" />
            {{ a.label }}
          </CommandItem>
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>
</template>
