<script setup lang="ts">
import type { Editor } from '@tiptap/vue-3'
import { ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'
import { safeUrl } from '~/editor/schema'

const props = defineProps<{ editor: Editor }>()
const open = defineModel<boolean>('open', { default: false })

const href = ref('')
const title = ref('')
const newTab = ref(false)
const nofollow = ref(false)
const error = ref('')

watch(open, (v) => {
  if (!v)
    return
  const a = props.editor.getAttributes('link')
  href.value = a.href ?? ''
  title.value = a.title ?? ''
  newTab.value = a.target === '_blank'
  nofollow.value = (a.rel ?? '').split(' ').includes('nofollow')
  error.value = ''
})

function apply() {
  const url = href.value.trim()
  if (!url || !safeUrl(url)) {
    error.value = 'Use an http(s), mailto:, tel:, relative or #anchor link.'
    return
  }
  const attrs = {
    href: url,
    target: newTab.value ? '_blank' : null,
    rel: [newTab.value && 'noopener', nofollow.value && 'nofollow'].filter(Boolean).join(' ') || null,
    title: title.value.trim() || null,
  }
  const chain = props.editor.chain().focus()
  if (props.editor.state.selection.empty && !props.editor.isActive('link'))
    chain.insertContent({ type: 'text', text: url, marks: [{ type: 'link', attrs }] }).run()
  else
    chain.extendMarkRange('link').setLink(attrs).run()
  open.value = false
}

function remove() {
  props.editor.chain().focus().extendMarkRange('link').unsetLink().run()
  open.value = false
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverAnchor as-child>
      <slot />
    </PopoverAnchor>
    <PopoverContent align="start" class="w-80">
      <form class="grid gap-3" @submit.prevent="apply">
        <div class="grid gap-1.5">
          <Label for="link-href">URL</Label>
          <Input id="link-href" v-model="href" placeholder="https://, /path or #anchor" autocomplete="off" :aria-invalid="!!error" />
          <FormError :message="error" />
        </div>
        <div class="grid gap-1.5">
          <Label for="link-title">Title <span class="text-muted-foreground">(optional)</span></Label>
          <Input id="link-title" v-model="title" />
        </div>
        <div class="flex flex-wrap gap-4 text-sm">
          <Label class="flex items-center gap-2 font-normal">
            <Checkbox v-model="newTab" />
            Open in new tab
          </Label>
          <Label class="flex items-center gap-2 font-normal">
            <Checkbox v-model="nofollow" />
            nofollow
          </Label>
        </div>
        <div class="flex justify-between gap-2">
          <Button type="button" variant="ghost" size="sm" :disabled="!editor.isActive('link')" @click="remove">
            Remove link
          </Button>
          <Button type="submit" size="sm">
            Apply
          </Button>
        </div>
      </form>
    </PopoverContent>
  </Popover>
</template>
