<script setup lang="ts">
import { ref } from 'vue'
import { Label } from '@/components/ui/label'
import { TagsInput, TagsInputInput, TagsInputItem, TagsInputItemDelete, TagsInputItemText } from '@/components/ui/tags-input'
import { cspDirectives, validCspHost } from './groups'

const props = defineProps<{ error?: string, errorField?: string }>()
const model = defineModel<Record<string, string[]>>({ default: () => ({}) })
const errors = ref<Record<string, string>>({})

function update(dir: string, values: unknown[]) {
  const list = values.map(v => String(v).trim().toLowerCase().replace(/\/+$/, ''))
  const bad = list.filter(h => !validCspHost(h))
  const good = [...new Set(list.filter(h => validCspHost(h)))].slice(0, 50)
  errors.value = { ...errors.value, [dir]: bad.length ? `"${bad[0]}" isn't an https origin like https://cdn.example.com or https://*.example.com.` : '' }
  const next = { ...model.value }
  if (good.length)
    next[dir] = good
  else
    delete next[dir]
  model.value = next
}

function errorFor(dir: string) {
  return errors.value[dir] || (props.errorField === `extra_csp_hosts.${dir}` ? props.error : '')
}
</script>

<template>
  <div class="grid gap-4">
    <p class="text-xs text-muted-foreground">
      https origins only, optionally with a leading <code>*.</code> wildcard. Keywords such as 'unsafe-inline' and a bare * are refused.
      Embed providers (YouTube, Vimeo, Spotify, SoundCloud, Google Maps) are already allowed in frame-src.
    </p>
    <div v-for="dir in cspDirectives" :key="dir" class="grid gap-1.5">
      <Label :for="`csp-${dir}`" class="font-mono text-xs">{{ dir }}</Label>
      <TagsInput
        :model-value="model[dir] ?? []"
        add-on-blur
        add-on-paste
        :aria-invalid="!!errorFor(dir)"
        @update:model-value="v => update(dir, v)"
      >
        <TagsInputItem v-for="h in model[dir] ?? []" :key="h" :value="h">
          <TagsInputItemText class="font-mono text-xs" />
          <TagsInputItemDelete />
        </TagsInputItem>
        <TagsInputInput :id="`csp-${dir}`" placeholder="https://cdn.example.com" class="min-w-48 font-mono text-xs" />
      </TagsInput>
      <FormError :message="errorFor(dir)" />
    </div>
  </div>
</template>
