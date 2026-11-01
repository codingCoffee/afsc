<script setup lang="ts" generic="T">
import { onMounted, ref, shallowRef, watch } from 'vue'
import { Command, CommandItem, CommandList } from '@/components/ui/command'
import { Input } from '@/components/ui/input'

// a server-searched list: the input queries `search`, results aren't filtered again client-side
const props = defineProps<{
  search: (q: string) => Promise<T[]>
  label: (item: T) => string
  hint?: (item: T) => string
  itemKey: (item: T) => string | number
  placeholder?: string
}>()
const emit = defineEmits<{ pick: [item: T] }>()

const q = ref('')
const results = shallowRef<T[]>([])
const loading = ref(false)
const error = ref('')

async function run() {
  loading.value = true
  error.value = ''
  try {
    results.value = await props.search(q.value.trim())
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Search failed.'
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(run, 250)
})
onMounted(run)
</script>

<template>
  <div class="grid gap-2">
    <Input v-model="q" :placeholder="placeholder ?? 'Search'" autofocus />
    <Command class="rounded-md border">
      <CommandList class="max-h-64">
        <CommandItem v-for="r in results" :key="itemKey(r)" :value="String(itemKey(r))" @select="emit('pick', r)">
          <span class="min-w-0 flex-1 truncate">{{ label(r) }}</span>
          <span v-if="hint" class="shrink-0 font-mono text-[11px] text-muted-foreground">{{ hint(r) }}</span>
        </CommandItem>
        <p v-if="!loading && !results.length" class="py-6 text-center text-sm text-muted-foreground">
          {{ error || 'No matches.' }}
        </p>
      </CommandList>
    </Command>
  </div>
</template>
