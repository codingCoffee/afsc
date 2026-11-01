<script setup lang="ts">
import type { Editor } from '@tiptap/vue-3'
import type { Component } from 'vue'
import {
  AlignCenter,
  AlignJustify,
  AlignLeft,
  AlignRight,
  Bold,
  Box,
  ChevronsDownUp,
  Clapperboard,
  Code,
  Ellipsis,
  Eraser,
  Film,
  Heading,
  Image,
  Images,
  Italic,
  Link,
  List,
  ListOrdered,
  Minus,
  Paintbrush,
  Pilcrow,
  Quote,
  Redo,
  Scissors,
  SquareCode,
  Strikethrough,
  Subscript,
  Superscript,
  Table,
  Underline,
  Undo,
} from '@lucide/vue'
import { computed, inject, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator, CommandShortcut } from '@/components/ui/command'
import { Input } from '@/components/ui/input'
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Separator } from '@/components/ui/separator'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { editorHostKey } from '~/editor/host'
import { isColor } from '~/editor/schema'
import LinkPopover from './LinkPopover.vue'

const props = defineProps<{ editor: Editor, allowedClasses: string[], canRawHtml: boolean }>()
const host = inject(editorHostKey)!
const linkOpen = defineModel<boolean>('linkOpen', { default: false })

const mod = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl+'
const e = computed(() => props.editor)
const run = () => e.value.chain().focus()

interface Tool {
  label: string
  icon: Component
  active?: () => boolean
  disabled?: () => boolean
  keys?: string
  action: () => void
  // shown in the bar from this breakpoint up; always listed in "more"
  bar?: 'all' | 'sm' | 'md'
}

async function insertImage() {
  const [m] = await host.pickMedia('image')
  if (m)
    run().insertImage({ mediaId: m.id, alt: m.alt, width: m.width, height: m.height }).run()
}

async function insertGallery() {
  const items = await host.pickMedia('image', true)
  if (items.length)
    run().insertGallery(items.map(m => ({ mediaId: m.id, alt: m.alt, width: m.width, height: m.height }))).run()
}

async function insertVideo() {
  const [m] = await host.pickMedia('video')
  if (m)
    run().insertVideo({ mediaId: m.id }).run()
}

const textTools = computed<Tool[]>(() => [
  { label: 'Bold', icon: Bold, keys: `${mod}B`, bar: 'all', active: () => e.value.isActive('bold'), action: () => run().toggleBold().run() },
  { label: 'Italic', icon: Italic, keys: `${mod}I`, bar: 'all', active: () => e.value.isActive('italic'), action: () => run().toggleItalic().run() },
  { label: 'Underline', icon: Underline, keys: `${mod}U`, bar: 'sm', active: () => e.value.isActive('underline'), action: () => run().toggleUnderline().run() },
  { label: 'Link', icon: Link, keys: `${mod}K`, bar: 'all', active: () => e.value.isActive('link'), action: () => linkOpen.value = true },
  { label: 'Strikethrough', icon: Strikethrough, keys: `${mod}Shift+S`, active: () => e.value.isActive('strike'), action: () => run().toggleStrike().run() },
  { label: 'Inline code', icon: Code, keys: `${mod}E`, active: () => e.value.isActive('code'), action: () => run().toggleCode().run() },
  { label: 'Subscript', icon: Subscript, keys: `${mod},`, active: () => e.value.isActive('subscript'), action: () => run().toggleSubscript().run() },
  { label: 'Superscript', icon: Superscript, keys: `${mod}.`, active: () => e.value.isActive('superscript'), action: () => run().toggleSuperscript().run() },
  { label: 'Text color…', icon: Paintbrush, active: () => !!e.value.getAttributes('textStyle').color, action: () => openColor('color') },
  { label: 'Background color…', icon: Paintbrush, active: () => !!e.value.getAttributes('textStyle').backgroundColor, action: () => openColor('backgroundColor') },
  { label: 'Clear formatting', icon: Eraser, action: () => run().unsetAllMarks().run() },
])

const blockTools = computed<Tool[]>(() => [
  { label: 'Paragraph', icon: Pilcrow, keys: `${mod}Alt+0`, active: () => e.value.isActive('paragraph'), action: () => run().setParagraph().run() },
  ...[1, 2, 3, 4, 5, 6].map(level => ({
    label: `Heading ${level}`,
    icon: Heading,
    keys: `${mod}Alt+${level}`,
    bar: level === 2 || level === 3 ? 'md' as const : undefined,
    active: () => e.value.isActive('heading', { level }),
    action: () => run().toggleHeading({ level: level as 1 | 2 | 3 | 4 | 5 | 6 }).run(),
  })),
  { label: 'Bulleted list', icon: List, keys: `${mod}Shift+8`, bar: 'sm', active: () => e.value.isActive('bulletList'), action: () => run().toggleBulletList().run() },
  { label: 'Numbered list', icon: ListOrdered, keys: `${mod}Shift+7`, bar: 'sm', active: () => e.value.isActive('orderedList'), action: () => run().toggleOrderedList().run() },
  { label: 'Quote', icon: Quote, keys: `${mod}Shift+B`, bar: 'md', active: () => e.value.isActive('blockquote'), action: () => run().toggleBlockquote().run() },
  { label: 'Code block', icon: SquareCode, keys: `${mod}Alt+C`, active: () => e.value.isActive('codeBlock'), action: () => run().toggleCodeBlock().run() },
  { label: 'Align left', icon: AlignLeft, keys: `${mod}Shift+L`, active: () => e.value.isActive({ textAlign: 'left' }), action: () => run().toggleTextAlign('left').run() },
  { label: 'Align center', icon: AlignCenter, keys: `${mod}Shift+E`, active: () => e.value.isActive({ textAlign: 'center' }), action: () => run().toggleTextAlign('center').run() },
  { label: 'Align right', icon: AlignRight, keys: `${mod}Shift+R`, active: () => e.value.isActive({ textAlign: 'right' }), action: () => run().toggleTextAlign('right').run() },
  { label: 'Justify', icon: AlignJustify, keys: `${mod}Shift+J`, active: () => e.value.isActive({ textAlign: 'justify' }), action: () => run().toggleTextAlign('justify').run() },
])

const insertTools = computed<Tool[]>(() => [
  { label: 'Image', icon: Image, bar: 'all', action: insertImage },
  { label: 'Gallery', icon: Images, action: insertGallery },
  { label: 'Video', icon: Film, action: insertVideo },
  { label: 'Embed', icon: Clapperboard, action: () => run().insertEmbed({}).run() },
  { label: 'Table', icon: Table, action: () => run().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run() },
  { label: 'Collapsible section', icon: ChevronsDownUp, action: () => run().setDetails().run() },
  { label: 'Horizontal rule', icon: Minus, action: () => run().setHorizontalRule().run() },
  { label: 'Read more break', icon: Scissors, action: () => run().insertReadMore().run() },
  ...(props.canRawHtml ? [{ label: 'Raw HTML', icon: SquareCode, action: () => run().insertRawHtml().run() }] : []),
])

const tableTools = computed<Tool[]>(() => !e.value.isActive('table')
  ? []
  : [
      { label: 'Add row below', icon: Table, action: () => run().addRowAfter().run() },
      { label: 'Add column right', icon: Table, action: () => run().addColumnAfter().run() },
      { label: 'Delete row', icon: Table, action: () => run().deleteRow().run() },
      { label: 'Delete column', icon: Table, action: () => run().deleteColumn().run() },
      { label: 'Toggle header row', icon: Table, action: () => run().toggleHeaderRow().run() },
      { label: 'Merge or split cells', icon: Table, action: () => run().mergeOrSplit().run() },
      { label: 'Delete table', icon: Table, action: () => run().deleteTable().run() },
    ])

const containerTools = computed<Tool[]>(() => [
  ...props.allowedClasses.map(c => ({
    label: `Container: ${c}`,
    icon: Box,
    active: () => e.value.isActive('container', { classes: c }),
    action: () => run().setContainer(c).run(),
  })),
  ...(e.value.isActive('container') ? [{ label: 'Remove container', icon: Box, action: () => run().unsetContainer().run() }] : []),
])

const barTools = computed(() => [textTools.value, blockTools.value, insertTools.value].map(g => g.filter(t => t.bar)))

const groups = computed(() => [
  { heading: 'Text', tools: textTools.value },
  { heading: 'Blocks', tools: blockTools.value },
  { heading: 'Insert', tools: insertTools.value },
  { heading: 'Table', tools: tableTools.value },
  { heading: 'Container', tools: containerTools.value },
].filter(g => g.tools.length))

const visibility = { all: 'inline-flex', sm: 'hidden sm:inline-flex', md: 'hidden md:inline-flex' }
const moreOpen = ref(false)
function pick(t: Tool) {
  moreOpen.value = false
  t.action()
}

const colorOpen = ref(false)
const colorKind = ref<'color' | 'backgroundColor'>('color')
const colorValue = ref('')
const swatches = ['#000000', '#555555', '#c62828', '#ad1457', '#6a1b9a', '#1565c0', '#00838f', '#2e7d32', '#f9a825', '#ef6c00', '#fff59d', '#c8e6c9']

function openColor(kind: 'color' | 'backgroundColor') {
  colorKind.value = kind
  colorValue.value = e.value.getAttributes('textStyle')[kind] ?? ''
  colorOpen.value = true
}

function applyColor(v: string | null) {
  const c = run()
  if (colorKind.value === 'color')
    (v ? c.setColor(v) : c.unsetColor()).run()
  else
    (v ? c.setBackgroundColor(v) : c.unsetBackgroundColor()).run()
  colorOpen.value = false
}
</script>

<template>
  <TooltipProvider :delay-duration="500">
    <div class="flex flex-wrap items-center gap-0.5 border-b bg-background/95 p-1 backdrop-blur" role="toolbar" aria-label="Formatting">
      <Tooltip>
        <TooltipTrigger as-child>
          <Button variant="ghost" size="icon-sm" aria-label="Undo" :disabled="!editor.can().undo()" @click="run().undo().run()">
            <Undo />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Undo {{ mod }}Z</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger as-child>
          <Button variant="ghost" size="icon-sm" class="hidden sm:inline-flex" aria-label="Redo" :disabled="!editor.can().redo()" @click="run().redo().run()">
            <Redo />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Redo {{ mod }}Shift+Z</TooltipContent>
      </Tooltip>
      <template v-for="(group, gi) in barTools" :key="gi">
        <Separator orientation="vertical" class="mx-1 !h-5" />
        <Tooltip v-for="t in group" :key="t.label">
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="icon-sm"
              :class="[visibility[t.bar!], t.active?.() && 'bg-accent text-accent-foreground']"
              :aria-label="t.label"
              :aria-pressed="t.active ? t.active() : undefined"
              @click="t.action()"
            >
              <component :is="t.icon" />
              <span v-if="t.label.startsWith('Heading')" class="-ml-1 font-mono text-[10px]">{{ t.label.slice(-1) }}</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>{{ t.label }} {{ t.keys }}</TooltipContent>
        </Tooltip>
      </template>
      <Separator orientation="vertical" class="mx-1 !h-5" />
      <LinkPopover v-model:open="linkOpen" :editor="editor">
        <span class="size-0" />
      </LinkPopover>
      <Popover v-model:open="colorOpen">
        <PopoverAnchor as-child>
          <span class="size-0" />
        </PopoverAnchor>
        <PopoverContent align="start" class="grid w-64 gap-3">
          <div class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
            {{ colorKind === 'color' ? 'Text color' : 'Background color' }}
          </div>
          <div class="grid grid-cols-6 gap-1.5">
            <button v-for="s in swatches" :key="s" type="button" class="size-7 rounded border" :style="{ background: s }" :aria-label="s" @click="applyColor(s)" />
          </div>
          <form class="flex gap-2" @submit.prevent="isColor(colorValue) && applyColor(colorValue)">
            <Input v-model="colorValue" placeholder="#rrggbb or rgb(…)" class="h-8 font-mono text-xs" :aria-invalid="!!colorValue && !isColor(colorValue)" />
            <Button type="submit" size="sm" :disabled="!isColor(colorValue)">
              Set
            </Button>
          </form>
          <Button variant="ghost" size="sm" @click="applyColor(null)">
            Clear
          </Button>
        </PopoverContent>
      </Popover>
      <Popover v-model:open="moreOpen">
        <PopoverTrigger as-child>
          <Button variant="ghost" size="sm" aria-label="More formatting and blocks">
            <Ellipsis />
            <span class="hidden sm:inline">More</span>
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" class="w-72 p-0">
          <Command>
            <CommandInput placeholder="Find a tool…" />
            <CommandList class="max-h-80">
              <CommandEmpty>Nothing matches.</CommandEmpty>
              <template v-for="(g, i) in groups" :key="g.heading">
                <CommandSeparator v-if="i" />
                <CommandGroup :heading="g.heading">
                  <CommandItem
                    v-for="t in g.tools"
                    :key="t.label"
                    :value="t.label"
                    :class="t.active?.() && 'font-medium'"
                    @select="pick(t)"
                  >
                    <component :is="t.icon" />
                    {{ t.label }}
                    <CommandShortcut v-if="t.keys">
                      {{ t.keys }}
                    </CommandShortcut>
                  </CommandItem>
                </CommandGroup>
              </template>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    </div>
  </TooltipProvider>
</template>
