<script setup lang="ts">
const year = new Date().getFullYear()

const links = [
  { label: 'Rules', to: { path: '/', hash: '#rules' } },
  { label: 'Install', to: { path: '/', hash: '#install' } },
  { label: 'Status', to: '/health' },
]
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
            active-class="text-foreground"
          >
            {{ link.label }}
          </NuxtLink>
        </nav>

        <ThemeToggle />
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
