<script setup lang="ts">
import { Braces } from '@lucide/vue'
import { nextTick, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { seoVars } from './seo'

const props = defineProps<{ id?: string, placeholder?: string, multiline?: boolean, invalid?: boolean }>()
// null and '' both mean "not set", so clearing the field falls back to the template
const model = defineModel<string | null>({ default: null })

const open = ref(false)
const field = ref<HTMLInputElement | HTMLTextAreaElement | null>(null)
let caret: [number, number] | null = null

function remember() {
  const el = field.value
  if (el)
    caret = [el.selectionStart ?? el.value.length, el.selectionEnd ?? el.value.length]
}

function update(e: Event) {
  model.value = (e.target as HTMLInputElement).value || null
}

async function insert(name: string) {
  open.value = false
  const cur = model.value ?? ''
  const [start, end] = caret ?? [cur.length, cur.length]
  const token = `%%${name}%%`
  const before = cur.slice(0, start)
  const pad = before && !before.endsWith(' ') ? ' ' : ''
  model.value = before + pad + token + cur.slice(end)
  await nextTick()
  const pos = start + pad.length + token.length
  field.value?.focus()
  field.value?.setSelectionRange(pos, pos)
  caret = [pos, pos]
}

const base = 'w-full min-w-0 rounded-md border border-input bg-transparent px-3 text-sm shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 aria-invalid:border-destructive dark:bg-input/30'
</script>

<template>
  <div class="flex items-start gap-1.5">
    <textarea
      v-if="props.multiline"
      :id="id"
      ref="field"
      :value="model ?? ''"
      :placeholder="placeholder"
      :aria-invalid="invalid || undefined"
      rows="3"
      :class="[base, 'min-h-16 py-2']"
      @input="update"
      @blur="remember"
      @keyup="remember"
      @click="remember"
    />
    <input
      v-else
      :id="id"
      ref="field"
      :value="model ?? ''"
      :placeholder="placeholder"
      :aria-invalid="invalid || undefined"
      :class="[base, 'h-9 py-1']"
      @input="update"
      @blur="remember"
      @keyup="remember"
      @click="remember"
    >
    <Popover v-model:open="open">
      <PopoverTrigger as-child>
        <Button variant="outline" size="icon" type="button" aria-label="Insert a variable" @mousedown="remember">
          <Braces />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" class="w-72 p-0">
        <Command>
          <CommandInput placeholder="Find a variable" />
          <CommandList class="max-h-72">
            <CommandEmpty>No variable matches.</CommandEmpty>
            <CommandItem v-for="v in seoVars" :key="v.name" :value="`${v.name} ${v.help}`" @select="insert(v.name)">
              <div class="grid min-w-0">
                <span class="font-mono text-xs">%%{{ v.name }}%%</span>
                <span class="truncate text-xs text-muted-foreground">{{ v.help }}</span>
              </div>
            </CommandItem>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  </div>
</template>
