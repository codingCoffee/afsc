<script setup lang="ts">
import { ArrowDownRight, ArrowRight, ArrowUp } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

const initialHeadline = 'Write. Save. Leave.'
const chars = ref(initialHeadline.length)
const saveState = ref<'saved' | 'saving'>('saved')
let saveTimer: ReturnType<typeof setTimeout> | undefined

function onHeadlineInput(e: Event) {
  chars.value = (e.target as HTMLElement).textContent?.length ?? 0
  saveState.value = 'saving'
  clearTimeout(saveTimer)
  saveTimer = setTimeout(() => (saveState.value = 'saved'), 700)
}

onBeforeUnmount(() => clearTimeout(saveTimer))

const readout = [
  { label: 'Binaries', value: '1' },
  { label: 'Databases', value: '1' },
  { label: 'Plugins', value: '0' },
  { label: 'Setup steps', value: '2' },
]

const rules = [
  { title: 'One binary', body: 'Admin, API and the site ship as a single executable. Copy it to a box, run it, done.' },
  { title: 'Postgres. Nothing else.', body: 'No cache tier, no search cluster, no queue. One database you already know how to back up.' },
  { title: 'Zero plugins to babysit', body: 'Everything it does is built in. Nothing to update at 2am because a dependency of a dependency broke.' },
  { title: 'Static by default', body: 'Pages are pre-built and served flat. Fast without a tuning guide.' },
  { title: 'An admin without a manual', body: 'If a screen needs a tutorial, the screen is wrong. We delete it and try again.' },
  { title: 'Migrations that talk', body: 'Every schema change says what it is doing, one step at a time, and stops exactly where you told it to.' },
]

const terminal = [
  { kind: 'cmd', text: './afsc migrate up' },
  { kind: 'out', text: 'Running migration 0001 (0001_init)' },
  { kind: 'out', text: 'Done, 1 migration(s), now at 0001' },
  { kind: 'cmd', text: './afsc serve --api-port 1381' },
  { kind: 'ok', text: 'http server started on [::]:1381' },
]
</script>

<template>
  <div>
    <section class="bg-grid relative overflow-hidden border-b">
      <div class="relative mx-auto grid max-w-7xl gap-12 px-4 pt-10 pb-16 sm:px-8 sm:pt-16 lg:grid-cols-[1fr_17rem] lg:gap-16 lg:pt-24 lg:pb-28">
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-x-6 gap-y-2 font-mono text-[11px] tracking-widest text-muted-foreground uppercase animate-rise">
            <span class="text-primary">N°001</span>
            <span>content / system</span>
            <span class="flex items-center gap-2">
              <span class="relative flex size-1.5">
                <span class="absolute inline-flex size-full animate-ping rounded-full bg-primary opacity-60" />
                <span class="relative inline-flex size-1.5 rounded-full bg-primary" />
              </span>
              armed
            </span>
          </div>

          <h1
            contenteditable="plaintext-only"
            spellcheck="false"
            aria-label="Editable headline"
            class="mt-8 font-display text-[clamp(2.1rem,8.5vw,6.75rem)] leading-[1.15] tracking-tight break-words uppercase caret-primary selection:bg-primary/20 selection:text-foreground outline-none animate-rise [animation-delay:120ms] focus:text-primary sm:mt-10"
            @input="onHeadlineInput"
          >{{ initialHeadline }}</h1>

          <div class="mt-5 flex flex-wrap items-center gap-3 animate-rise [animation-delay:220ms]">
            <span class="flex items-center gap-2 font-mono text-[11px] tracking-widest text-primary uppercase">
              <ArrowUp class="size-3.5" />
              click it. edit it. that's the cms.
            </span>
            <Badge variant="outline" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
              {{ chars }} chars
            </Badge>
            <Badge variant="outline" class="rounded-sm font-mono text-[10px] tracking-widest uppercase">
              <span :class="saveState === 'saved' ? 'text-ok' : 'text-muted-foreground animate-pulse'">
                {{ saveState === 'saved' ? 'saved ✓' : 'saving…' }}
              </span>
            </Badge>
          </div>

          <p class="mt-10 max-w-xl text-base text-pretty text-muted-foreground animate-rise [animation-delay:320ms] sm:text-lg">
            AFSC is a content system that fits in one binary and one Postgres database.
            No marketplace, no theme economy, no forty-step setup wizard.
            You write, it publishes, you go outside.
          </p>

          <div class="mt-10 flex flex-wrap gap-3 animate-rise [animation-delay:420ms]">
            <Button as-child size="lg" class="font-mono text-xs tracking-widest uppercase">
              <NuxtLink :to="{ path: '/', hash: '#install' }">
                Install
                <ArrowDownRight />
              </NuxtLink>
            </Button>
            <Button as-child size="lg" variant="outline" class="font-mono text-xs tracking-widest uppercase">
              <NuxtLink to="/health">
                System status
                <ArrowRight />
              </NuxtLink>
            </Button>
          </div>
        </div>

        <aside class="self-end border-l pl-6 animate-rise [animation-delay:520ms] lg:pl-8">
          <p class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
            spec / readout
          </p>
          <dl class="mt-6 grid grid-cols-2 gap-x-6 gap-y-7 lg:grid-cols-1">
            <div v-for="item in readout" :key="item.label" class="group">
              <dt class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
                {{ item.label }}
              </dt>
              <dd class="mt-1 font-display text-4xl transition-colors group-hover:text-primary">
                {{ item.value }}
              </dd>
            </div>
          </dl>
        </aside>
      </div>
    </section>

    <section id="rules" class="mx-auto max-w-7xl scroll-mt-14 px-4 py-20 sm:px-8 sm:py-28">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p class="font-mono text-[11px] tracking-widest text-primary uppercase">
            § 02 — ruleset
          </p>
          <h2 class="mt-4 font-display text-2xl tracking-tight uppercase sm:text-4xl">
            Six rules.<br>No exceptions.
          </h2>
        </div>
        <p class="max-w-xs font-mono text-xs text-muted-foreground">
          Every feature request is checked against these. Most of them lose.
        </p>
      </div>

      <ol class="mt-12 grid gap-px overflow-hidden border bg-border sm:grid-cols-2 lg:grid-cols-3">
        <li
          v-for="(rule, i) in rules"
          :key="rule.title"
          class="group relative bg-background p-6 transition-colors hover:bg-card sm:p-8"
        >
          <span class="font-mono text-xs text-primary">{{ String(i + 1).padStart(2, '0') }}</span>
          <h3 class="mt-6 font-display text-sm leading-relaxed tracking-wide uppercase">
            {{ rule.title }}
          </h3>
          <p class="mt-3 text-sm text-pretty text-muted-foreground">
            {{ rule.body }}
          </p>
          <span aria-hidden="true" class="absolute top-0 left-0 h-px w-0 bg-primary transition-all duration-500 group-hover:w-full" />
        </li>
      </ol>
    </section>

    <section id="install" class="scroll-mt-14 border-y bg-card/50">
      <div class="mx-auto grid max-w-7xl gap-12 px-4 py-20 sm:px-8 sm:py-28 lg:grid-cols-[1fr_1.2fr] lg:items-center">
        <div>
          <p class="font-mono text-[11px] tracking-widest text-primary uppercase">
            § 03 — install
          </p>
          <h2 class="mt-4 font-display text-2xl tracking-tight uppercase sm:text-4xl">
            Two commands.
          </h2>
          <p class="mt-6 max-w-md text-muted-foreground">
            Point <code class="font-mono text-sm text-foreground">DATABASE_URL</code> at Postgres,
            run the migrations, start the server. There is no step three.
          </p>
        </div>

        <div class="min-w-0 overflow-hidden rounded-md border bg-background shadow-[0_30px_80px_-50px_var(--primary)]">
          <div class="flex items-center justify-between border-b px-4 py-2.5 font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
            <span>tty0 — afsc</span>
            <span class="text-primary">● live</span>
          </div>
          <pre class="overflow-x-auto p-5 font-mono text-[13px] leading-7 sm:p-6"><code><span
            v-for="(line, i) in terminal"
            :key="i"
            class="block animate-rise"
            :style="{ animationDelay: `${300 + i * 260}ms` }"
          ><template v-if="line.kind === 'cmd'"><span class="text-primary select-none">$ </span><span class="text-foreground">{{ line.text }}</span></template><span
            v-else
            :class="line.kind === 'ok' ? 'text-ok' : 'text-muted-foreground'"
          >  {{ line.text }}</span></span><span class="block animate-rise" :style="{ animationDelay: `${300 + terminal.length * 260}ms` }"><span class="text-primary select-none">$ </span><span class="inline-block h-4 w-2 translate-y-0.5 animate-blink bg-primary" /></span></code></pre>
        </div>
      </div>
    </section>

    <section class="mx-auto max-w-7xl px-4 py-24 sm:px-8 sm:py-32">
      <p class="max-w-4xl font-display text-xl leading-snug tracking-tight text-balance uppercase sm:text-3xl lg:text-4xl">
        Your words don't need an ecosystem.
        <span class="text-primary">They need a save button.</span>
      </p>
      <Button as-child variant="link" class="mt-8 px-0 font-mono text-xs tracking-widest uppercase">
        <NuxtLink to="/health">
          Check the pulse
          <ArrowRight />
        </NuxtLink>
      </Button>
    </section>
  </div>
</template>
