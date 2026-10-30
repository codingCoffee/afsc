<script setup lang="ts">
defineProps<{ code: string, title: string }>()
</script>

<template>
  <section class="bg-grid border-b">
    <div class="mx-auto grid max-w-7xl gap-10 px-4 py-12 sm:px-8 sm:py-20 lg:grid-cols-[1fr_28rem] lg:items-start lg:gap-16">
      <div class="animate-rise">
        <p class="font-mono text-[11px] tracking-widest text-primary uppercase">
          {{ code }}
        </p>
        <h1 class="mt-4 font-display text-4xl tracking-tight uppercase sm:text-6xl">
          {{ title }}
        </h1>
        <p class="mt-4 max-w-sm text-muted-foreground">
          One account. No onboarding tour.
        </p>
      </div>

      <div class="animate-rise rounded-md border bg-card p-6 [animation-delay:120ms] sm:p-8">
        <slot />
        <p v-if="$slots.footer" class="mt-8 border-t pt-5 font-mono text-xs text-muted-foreground">
          <slot name="footer" />
        </p>
      </div>
    </div>
  </section>
</template>
