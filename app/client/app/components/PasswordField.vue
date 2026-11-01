<script setup lang="ts">
import { Check, Minus } from '@lucide/vue'
import { computed } from 'vue'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const props = defineProps<{ id: string, label: string, error?: string, autocomplete?: string }>()
const model = defineModel<string>({ required: true })

// the server enforces the same rules outside dev
const strict = !import.meta.dev

const rules = computed(() => [
  { label: '8+ characters', ok: [...model.value].length >= 8 },
  { label: 'Uppercase', ok: /\p{Lu}/u.test(model.value) },
  { label: 'Lowercase', ok: /\p{Ll}/u.test(model.value) },
  { label: 'Number', ok: /\p{Nd}/u.test(model.value) },
  { label: 'Special', ok: /[\p{P}\p{S}]/u.test(model.value) },
])

const ok = computed(() => model.value !== '' && new TextEncoder().encode(model.value).length <= 72
  && (!strict || rules.value.every(r => r.ok)))

defineExpose({ ok })
</script>

<template>
  <div class="grid gap-2">
    <Label :for="props.id" class="font-mono text-[11px] tracking-widest text-muted-foreground uppercase">{{ props.label }}</Label>
    <Input :id="props.id" v-model="model" type="password" :autocomplete="props.autocomplete ?? 'new-password'" :aria-invalid="!!props.error" required />
    <p v-if="props.error" class="font-mono text-[11px] text-destructive">
      {{ props.error }}
    </p>
    <ul v-if="strict" class="mt-1 flex flex-wrap gap-x-4 gap-y-1.5 font-mono text-[11px] tracking-wide uppercase">
      <li v-for="rule in rules" :key="rule.label" class="flex items-center gap-1.5" :class="rule.ok ? 'text-ok' : 'text-muted-foreground'">
        <component :is="rule.ok ? Check : Minus" class="size-3" />
        {{ rule.label }}
      </li>
    </ul>
  </div>
</template>
