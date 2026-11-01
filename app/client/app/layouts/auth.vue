<template>
  <div class="grain relative flex min-h-svh flex-col">
    <header class="border-b">
      <div class="mx-auto flex h-14 max-w-7xl items-center gap-4 px-4 sm:px-8">
        <span class="font-display text-sm tracking-[0.2em]">AFSC</span>
        <div class="ml-auto">
          <ThemeToggle />
        </div>
      </div>
    </header>
    <main class="flex-1">
      <slot />
    </main>
  </div>
</template>
