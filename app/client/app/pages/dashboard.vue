<script setup lang="ts">
import type { Dashboard, PostListItem, PostStatus } from '@/types/api'
import { computed, ref } from 'vue'
import { useCspReportOnly } from '@/components/settings/groups'
import { API } from '@/composables/useAuth'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

definePageMeta({ middleware: 'auth' })
useHead({ title: 'Dashboard · AFSC' })

const { user, authFetch, can } = useAuth()
const { reportOnly } = useCspReportOnly()

const data = ref<Dashboard | null>(null)
const error = ref('')
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const day = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })

async function load() {
  try {
    data.value = await authFetch<Dashboard>(`${API}/dashboard`)
    reportOnly.value = data.value.csp_report_only
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load the dashboard.'
  }
}
load()

const statuses: PostStatus[] = ['published', 'scheduled', 'pending', 'draft', 'private', 'trash']
const seesAll = computed(() => can('posts.edit_others'))

const lists = computed(() => {
  const d = data.value
  if (!d)
    return []
  return [
    { title: 'My drafts', description: 'Your drafts and posts waiting for review.', items: d.my_drafts, empty: 'No drafts.', when: (p: PostListItem) => p.updated_at },
    { title: 'Pending review', description: seesAll.value ? 'Submitted by contributors.' : 'Your posts waiting for an editor.', items: d.pending_review, empty: 'Nothing waiting.', when: (p: PostListItem) => p.updated_at },
    { title: 'Scheduled', description: 'Next to publish.', items: d.scheduled, empty: 'Nothing scheduled.', when: (p: PostListItem) => p.published_at },
  ]
})

function when(v: string | null) {
  return v ? fmt.format(new Date(v)) : ''
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Dashboard" :description="user ? `Signed in as ${user.display_name || user.username} (${user.role}).` : ''" />

    <FormError :message="error" />

    <section v-if="data" aria-label="Post counts" class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      <div v-for="s in statuses" :key="s" class="rounded-md border px-3 py-2.5">
        <p class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
          {{ s }}
        </p>
        <p class="mt-1 font-display text-xl tabular-nums">
          {{ data.counts[s] ?? 0 }}
        </p>
      </div>
    </section>
    <p v-if="data && !seesAll" class="-mt-3 text-xs text-muted-foreground">
      Counts cover your own posts.
    </p>

    <div v-if="!data && !error" class="grid gap-4 md:grid-cols-2">
      <Skeleton v-for="i in 4" :key="i" class="h-48" />
    </div>

    <div v-if="data" class="grid gap-4 md:grid-cols-2">
      <Card v-for="l in lists" :key="l.title" class="gap-3">
        <CardHeader>
          <CardTitle class="font-display text-sm tracking-wide uppercase">
            {{ l.title }}
          </CardTitle>
          <CardDescription>{{ l.description }}</CardDescription>
        </CardHeader>
        <CardContent>
          <p v-if="!l.items.length" class="text-sm text-muted-foreground">
            {{ l.empty }}
          </p>
          <ul v-else class="divide-y">
            <li v-for="p in l.items" :key="p.id" class="flex items-baseline gap-3 py-2">
              <NuxtLink :to="`/posts/${p.id}`" class="min-w-0 flex-1 truncate text-sm hover:underline">
                {{ p.title || '(no title)' }}
              </NuxtLink>
              <span class="hidden shrink-0 text-xs text-muted-foreground sm:inline">{{ p.authors.map(a => a.display_name).join(', ') }}</span>
              <time class="shrink-0 font-mono text-[11px] text-muted-foreground" :datetime="l.when(p) ?? undefined">{{ when(l.when(p)) }}</time>
            </li>
          </ul>
        </CardContent>
      </Card>

      <Card v-if="data.pending_comments" class="gap-3">
        <CardHeader>
          <CardTitle class="font-display text-sm tracking-wide uppercase">
            Pending comments <span class="font-mono text-xs tabular-nums text-muted-foreground">{{ data.pending_comments.count }}</span>
          </CardTitle>
          <CardDescription>
            Waiting for moderation. <NuxtLink to="/comments" class="underline underline-offset-2">Open comments</NuxtLink>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <p v-if="!data.pending_comments.latest.length" class="text-sm text-muted-foreground">
            Nothing to moderate.
          </p>
          <ul v-else class="divide-y">
            <li v-for="c in data.pending_comments.latest" :key="c.id" class="grid gap-0.5 py-2">
              <div class="flex items-baseline gap-3">
                <span class="min-w-0 flex-1 truncate text-sm font-medium">{{ c.author_name }}</span>
                <time class="shrink-0 font-mono text-[11px] text-muted-foreground" :datetime="c.created_at">{{ when(c.created_at) }}</time>
              </div>
              <p class="line-clamp-2 text-sm text-muted-foreground">
                {{ c.excerpt }}
              </p>
              <NuxtLink :to="{ path: '/comments', query: { post_id: c.post_id } }" class="truncate text-xs underline-offset-2 hover:underline">
                On {{ c.post_title }}
              </NuxtLink>
            </li>
          </ul>
        </CardContent>
      </Card>

      <Card v-if="can('content.manage')" class="gap-3">
        <CardHeader>
          <CardTitle class="font-display text-sm tracking-wide uppercase">
            Top 404s
          </CardTitle>
          <CardDescription>
            Most-hit missing URLs. <NuxtLink to="/seo/not-found" class="underline underline-offset-2">Open the 404 log</NuxtLink>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <p v-if="!data.top_404s.length" class="text-sm text-muted-foreground">
            No 404s logged.
          </p>
          <ul v-else class="divide-y">
            <li v-for="r in data.top_404s" :key="r.path" class="flex items-baseline gap-3 py-2">
              <span class="min-w-0 flex-1 truncate font-mono text-xs" :title="r.path">{{ r.path }}</span>
              <span class="shrink-0 font-mono text-xs tabular-nums">{{ r.hits }}</span>
              <span class="hidden shrink-0 text-xs text-muted-foreground sm:inline">{{ day.format(new Date(r.last_seen)) }}</span>
            </li>
          </ul>
        </CardContent>
      </Card>
    </div>
  </div>
</template>
