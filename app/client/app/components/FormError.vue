<script setup lang="ts">
defineProps<{ message?: string }>()
</script>

<template>
  <p v-if="message" role="alert" class="font-mono text-xs text-destructive">
    {{ message }}
  </p>
</template>
