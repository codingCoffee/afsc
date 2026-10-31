<script setup lang="ts">
import { ArrowLeft } from '@lucide/vue'
import { Button } from '@/components/ui/button'
</script>

<template>
  <section class="bg-grid border-b">
    <div class="mx-auto max-w-7xl px-4 pt-12 pb-24 sm:px-8 sm:pt-20 sm:pb-32">
      <p class="font-mono text-[11px] tracking-widest text-destructive uppercase animate-rise">
        § error — 403
      </p>
      <h1 class="mt-6 font-display text-[clamp(2.5rem,9vw,6rem)] leading-[1.05] tracking-tight uppercase animate-rise [animation-delay:120ms]">
        Admins<br>only.
      </h1>
      <p class="mt-6 max-w-md text-muted-foreground animate-rise [animation-delay:220ms]">
        Your account can't see this page. Ask an admin if you think it should.
      </p>
      <Button as-child variant="outline" class="mt-10 font-mono text-xs tracking-widest uppercase animate-rise [animation-delay:320ms]">
        <NuxtLink to="/dashboard">
          <ArrowLeft />
          Dashboard
        </NuxtLink>
      </Button>
    </div>
  </section>
</template>
