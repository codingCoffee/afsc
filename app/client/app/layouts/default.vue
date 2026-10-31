<script setup lang="ts">
import { LayoutDashboard, LogIn, LogOut } from '@lucide/vue'
import { computed } from 'vue'
import { Button } from '@/components/ui/button'

const { user, signout } = useAuth()

const year = new Date().getFullYear()

const links = computed(() => [
  { label: 'Rules', to: { path: '/', hash: '#rules' }, mobile: false },
  { label: 'Install', to: { path: '/', hash: '#install' }, mobile: false },
  { label: 'Status', to: '/health', mobile: true },
  ...(user.value?.user_type === 'admin'
    ? [
        { label: 'Users', to: '/model/users', mobile: false },
        { label: 'Tokens', to: '/model/tokens', mobile: false },
      ]
    : []),
])
</script>

<template>
  <div class="grain relative flex min-h-svh flex-col">
    <header class="sticky top-0 z-40 border-b bg-background/85 backdrop-blur-md">
      <div class="mx-auto flex h-14 max-w-7xl items-center gap-4 px-4 sm:px-8">
        <NuxtLink to="/" class="group flex items-baseline gap-3">
          <span class="font-display text-sm tracking-[0.2em] transition-colors group-hover:text-primary">AFSC</span>
          <span class="hidden font-mono text-[10px] tracking-widest text-muted-foreground uppercase sm:inline">rev 0.1 / sys.cms</span>
        </NuxtLink>

        <nav class="ml-auto flex items-center gap-4 sm:gap-7">
          <NuxtLink
            v-for="link in links"
            :key="link.label"
            :to="link.to"
            class="font-mono text-[11px] tracking-widest text-muted-foreground uppercase transition-colors hover:text-foreground"
            :class="{ 'hidden md:inline': !link.mobile }"
            active-class="text-foreground"
          >
            {{ link.label }}
          </NuxtLink>
        </nav>

        <div class="flex items-center gap-1">
          <template v-if="user">
            <Button as-child variant="ghost" size="sm" class="font-mono text-[11px] tracking-widest uppercase">
              <NuxtLink to="/dashboard" :aria-label="`Dashboard for ${user.username}`">
                <LayoutDashboard />
                <span class="hidden max-w-32 truncate sm:inline">{{ user.username }}</span>
              </NuxtLink>
            </Button>
            <Button variant="ghost" size="sm" class="font-mono text-[11px] tracking-widest uppercase" aria-label="Sign out" @click="signout">
              <LogOut />
              <span class="hidden sm:inline">Sign out</span>
            </Button>
          </template>
          <Button v-else as-child size="sm" class="font-mono text-[11px] tracking-widest uppercase">
            <NuxtLink to="/signin">
              <LogIn />
              Sign in
            </NuxtLink>
          </Button>
          <ThemeToggle />
        </div>
      </div>
    </header>

    <main class="flex-1">
      <slot />
    </main>

    <footer class="border-t">
      <div class="mx-auto max-w-7xl px-4 pt-10 pb-6 sm:px-8">
        <div class="flex flex-wrap items-center justify-between gap-4 font-mono text-[11px] tracking-widest text-muted-foreground uppercase">
          <span>© {{ year }} AFSC</span>
          <NuxtLink to="/health" class="flex items-center gap-2 transition-colors hover:text-foreground">
            <span class="size-1.5 rounded-full bg-primary" />
            system status
          </NuxtLink>
          <span>built to be ignored</span>
        </div>
        <p aria-hidden="true" class="text-outline mt-8 font-display text-[23vw] leading-[0.8] tracking-tight opacity-25 select-none xl:text-[17rem]">
          AFSC
        </p>
      </div>
    </footer>
  </div>
</template>
