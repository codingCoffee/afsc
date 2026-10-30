<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { Badge } from '@/components/ui/badge'

definePageMeta({ middleware: 'auth' })
useHead({ title: 'Dashboard · AFSC' })

const { user, fetchMe } = useAuth()

const name = computed(() => user.value?.first_name || user.value?.username)

onMounted(() => {
  fetchMe().catch(() => {})
})
</script>

<template>
  <section v-if="user" class="bg-grid border-b">
    <div class="mx-auto max-w-7xl px-4 pt-12 pb-24 sm:px-8 sm:pt-20 sm:pb-32">
      <div class="flex flex-wrap items-center gap-x-6 gap-y-2 font-mono text-[11px] tracking-widest text-muted-foreground uppercase animate-rise">
        <span class="text-primary">§ dashboard</span>
        <span>@{{ user.username }}</span>
        <span class="hidden sm:inline">{{ user.email }}</span>
        <Badge
          :variant="user.user_type === 'admin' ? 'default' : 'outline'"
          class="rounded-sm font-mono text-[10px] tracking-widest uppercase"
        >
          {{ user.user_type }}
        </Badge>
      </div>

      <h1 class="mt-8 font-display text-[clamp(2rem,7vw,5.5rem)] leading-[1.1] tracking-tight break-words uppercase animate-rise [animation-delay:120ms]">
        Welcome,<br>
        <span class="text-primary">{{ name }}.</span>
      </h1>

      <p class="mt-8 max-w-md text-muted-foreground animate-rise [animation-delay:220ms]">
        Nothing here yet. That's the point.
      </p>
    </div>
  </section>
</template>
