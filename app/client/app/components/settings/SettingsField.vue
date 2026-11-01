<script setup lang="ts">
import type { FieldDesc } from './groups'
import { computed } from 'vue'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import CspHosts from './CspHosts.vue'
import PagePicker from './PagePicker.vue'

const props = defineProps<{ field: FieldDesc, error?: string, errorField?: string }>()
const model = defineModel<any>()

const id = computed(() => `f-${props.field.key}`)
const zones = computed(() => props.field.kind === 'timezone'
  ? ['UTC', ...Intl.supportedValuesOf('timeZone').filter(z => z !== 'UTC')]
  : [])

function setNumber(v: string | number) {
  model.value = v === '' ? null : Number(v)
}

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <div v-if="field.kind === 'switch'" class="flex items-start justify-between gap-4 rounded-md border p-3">
    <div class="grid gap-1">
      <Label :for="id" class="text-sm">{{ field.label }}</Label>
      <p v-if="field.help" class="text-xs text-muted-foreground">
        {{ field.help }}
      </p>
      <FormError :message="error" />
    </div>
    <Switch :id="id" v-model="model" />
  </div>

  <div v-else class="grid gap-2">
    <Label :for="id" :class="labelClass">{{ field.label }}</Label>
    <Input
      v-if="field.kind === 'text'"
      :id="id"
      v-model="model"
      :placeholder="field.placeholder"
      :aria-invalid="!!error"
    />
    <Input
      v-else-if="field.kind === 'number'"
      :id="id"
      type="number"
      inputmode="numeric"
      class="w-40"
      :min="field.min"
      :max="field.max"
      :model-value="model ?? ''"
      :aria-invalid="!!error"
      @update:model-value="setNumber"
    />
    <Input v-else-if="field.kind === 'color'" :id="id" v-model="model" type="color" class="h-9 w-20 p-1" :aria-invalid="!!error" />
    <NativeSelect v-else-if="field.kind === 'select'" :id="id" v-model="model" :aria-invalid="!!error">
      <NativeSelectOption v-for="o in field.options" :key="o" :value="o">
        {{ o }}
      </NativeSelectOption>
    </NativeSelect>
    <NativeSelect v-else-if="field.kind === 'timezone'" :id="id" v-model="model" :aria-invalid="!!error">
      <NativeSelectOption v-for="z in zones" :key="z" :value="z">
        {{ z }}
      </NativeSelectOption>
    </NativeSelect>
    <PagePicker v-else-if="field.kind === 'page'" :id="id" v-model="model" :invalid="!!error" />
    <Textarea
      v-else-if="field.kind === 'code'"
      :id="id"
      v-model="model"
      rows="10"
      spellcheck="false"
      class="font-mono text-xs"
      :aria-invalid="!!error"
    />
    <CspHosts v-else-if="field.kind === 'csp'" v-model="model" :error="error" :error-field="errorField" />
    <p v-if="field.help" class="text-xs text-muted-foreground">
      {{ field.help }}
    </p>
    <FormError v-if="field.kind !== 'csp'" :message="error" />
  </div>
</template>
