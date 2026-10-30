<script setup lang="ts">
import { Check, Monitor, Moon, Sun } from '@lucide/vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Command, CommandGroup, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'

type Mode = 'system' | 'light' | 'dark'

const modes = [
  { value: 'system', label: 'System', icon: Monitor },
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
] as const

const colorMode = useColorMode()
const open = ref(false)
const pinned = ref(false)
const openedBy = ref<'hover' | 'click'>('click')
const anchor = ref<HTMLElement>()
let closeTimer: ReturnType<typeof setTimeout> | undefined

const current = computed(() => modes.find(m => m.value === colorMode.preference) ?? modes[0])

function cancelClose() {
  clearTimeout(closeTimer)
}

function close() {
  cancelClose()
  open.value = false
  pinned.value = false
}

// only real mice hover; touch fires pointerenter right before click, which would double-toggle
function onEnter(e: PointerEvent) {
  if (e.pointerType !== 'mouse')
    return
  cancelClose()
  if (!open.value) {
    openedBy.value = 'hover'
    open.value = true
  }
}

function onLeave(e: PointerEvent) {
  if (e.pointerType !== 'mouse' || pinned.value)
    return
  cancelClose()
  closeTimer = setTimeout(close, 150)
}

function onClick() {
  cancelClose()
  if (open.value && !pinned.value) {
    pinned.value = true
    return
  }
  if (open.value)
    return close()
  openedBy.value = 'click'
  open.value = true
  pinned.value = true
}

function onOpenChange(value: boolean) {
  if (!value)
    close()
}

// the button lives outside the content, so stop reka from treating its clicks as "outside"
function onInteractOutside(e: CustomEvent<{ originalEvent: Event }>) {
  const target = e.detail.originalEvent.target
  if (target instanceof Node && anchor.value?.contains(target))
    e.preventDefault()
}

function onAutoFocus(e: Event) {
  if (openedBy.value === 'hover')
    e.preventDefault()
}

function choose(mode: Mode) {
  colorMode.preference = mode
  close()
}

onBeforeUnmount(cancelClose)
</script>

<template>
  <Popover :open="open" @update:open="onOpenChange">
    <PopoverAnchor as-child>
      <div ref="anchor" class="inline-flex" @pointerenter="onEnter" @pointerleave="onLeave">
        <Button
          variant="ghost"
          size="icon-sm"
          :aria-label="`Theme: ${current.label}`"
          aria-haspopup="listbox"
          :aria-expanded="open"
          :data-state="open ? 'open' : 'closed'"
          class="data-[state=open]:bg-accent data-[state=open]:text-primary"
          @click="onClick"
        >
          <component :is="current.icon" />
        </Button>
      </div>
    </PopoverAnchor>
    <PopoverContent
      align="end"
      :side-offset="6"
      class="w-44 p-1"
      @pointerenter="onEnter"
      @pointerleave="onLeave"
      @interact-outside="onInteractOutside"
      @open-auto-focus="onAutoFocus"
      @close-auto-focus="onAutoFocus"
    >
      <Command :model-value="colorMode.preference">
        <CommandList>
          <CommandGroup heading="Theme">
            <CommandItem
              v-for="mode in modes"
              :key="mode.value"
              :value="mode.value"
              class="font-mono text-xs tracking-wider uppercase"
              @select="choose(mode.value)"
            >
              <component :is="mode.icon" />
              {{ mode.label }}
              <Check v-if="colorMode.preference === mode.value" class="ml-auto text-primary" />
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>
</template>
