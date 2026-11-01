<script setup lang="ts">
defineProps<{ title: string, description?: string }>()
</script>

<template>
  <header class="flex flex-col gap-4 border-b pb-5 sm:flex-row sm:items-end sm:justify-between">
    <div class="min-w-0">
      <h1 class="font-display text-xl tracking-tight uppercase sm:text-2xl">
        {{ title }}
      </h1>
      <p v-if="description" class="mt-1.5 max-w-2xl text-sm text-muted-foreground">
        {{ description }}
      </p>
    </div>
    <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2">
      <slot name="actions" />
    </div>
  </header>
</template>
