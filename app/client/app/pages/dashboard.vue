<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { ArrowUpRight } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'

definePageMeta({ middleware: 'auth' })
useHead({ title: 'Dashboard · AFSC' })

const { user, fetchMe } = useAuth()

const models = [
  { code: 'model / 01', label: 'Users', to: '/model/users', body: 'Create and remove accounts.' },
  { code: 'model / 02', label: 'Tokens', to: '/model/tokens', body: 'See live sessions, blacklist or clean up.' },
]

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
          :variant="user.role === 'admin' ? 'default' : 'outline'"
          class="rounded-sm font-mono text-[10px] tracking-widest uppercase"
        >
          {{ user.role }}
        </Badge>
      </div>

      <h1 class="mt-8 font-display text-[clamp(2rem,7vw,5.5rem)] leading-[1.1] tracking-tight break-words uppercase animate-rise [animation-delay:120ms]">
        Welcome,<br>
        <span class="text-primary">{{ name }}.</span>
      </h1>

      <p class="mt-8 max-w-md text-muted-foreground animate-rise [animation-delay:220ms]">
        Nothing here yet. That's the point.
      </p>

      <div v-if="user.role === 'admin'" class="mt-14 grid max-w-2xl gap-px overflow-hidden rounded-md border bg-border animate-rise [animation-delay:320ms] sm:grid-cols-2">
        <NuxtLink
          v-for="m in models"
          :key="m.to"
          :to="m.to"
          class="group bg-background p-6 transition-colors hover:bg-card"
        >
          <span class="font-mono text-[10px] tracking-widest text-primary uppercase">{{ m.code }}</span>
          <span class="mt-4 flex items-center justify-between font-display text-sm tracking-wide uppercase">
            {{ m.label }}
            <ArrowUpRight class="size-4 text-muted-foreground transition-colors group-hover:text-primary" />
          </span>
          <span class="mt-2 block text-sm text-muted-foreground">{{ m.body }}</span>
        </NuxtLink>
      </div>
    </div>
  </section>
</template>
