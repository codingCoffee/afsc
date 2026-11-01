<script setup lang="ts">
import type { SeoFields, SeoResolved, SeoResolveInput } from '@/types/api'
import { Copy, ExternalLink, LoaderCircle, TriangleAlert } from '@lucide/vue'
import { onBeforeUnmount, ref, watch } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { API, errorData } from '@/composables/useAuth'
import { copyText } from './seo'
import SeoChecks from './SeoChecks.vue'
import SeoLengthMeter from './SeoLengthMeter.vue'
import SeoMediaField from './SeoMediaField.vue'
import SeoSnippet from './SeoSnippet.vue'
import SeoSocialCard from './SeoSocialCard.vue'
import SeoVarInput from './SeoVarInput.vue'
import SeoVideos from './SeoVideos.vue'

type ResolvePost = NonNullable<SeoResolveInput['post']>

const props = defineProps<{ post: ResolvePost, postId: number | 'new' }>()
const seo = defineModel<SeoFields>('seo', { required: true })

const { authFetch } = useAuth()
const resolved = ref<SeoResolved | null>(null)
const resolving = ref(false)
const problem = ref<{ field?: string, message: string } | null>(null)

function set<K extends keyof SeoFields>(key: K, value: SeoFields[K]) {
  seo.value = { ...seo.value, [key]: value }
}

function text(key: 'focus_keyphrase' | 'canonical' | 'breadcrumb_title', v: string | number) {
  set(key, String(v).trim() ? String(v) : null)
}

// only the fields SeoResolveInput.post names; the server rejects unknown ones
function draft(p: ResolvePost): ResolvePost {
  return {
    type: p.type,
    title: p.title,
    slug: p.slug,
    excerpt: p.excerpt,
    content: p.content,
    featured_media_id: p.featured_media_id,
    category_ids: p.category_ids,
    primary_category_id: p.primary_category_id,
    published_at: p.published_at,
    parent_id: p.parent_id,
  }
}

let seq = 0
async function resolve() {
  const mine = ++seq
  resolving.value = true
  const body: SeoResolveInput = {
    entity_type: 'post',
    entity_id: props.postId === 'new' ? 0 : props.postId,
    post: draft(props.post),
    seo: seo.value,
  }
  try {
    const res = await authFetch<SeoResolved>(`${API}/seo/resolve`, { method: 'POST', body })
    if (mine === seq) {
      resolved.value = res
      problem.value = null
    }
  }
  catch (e) {
    if (mine === seq) {
      const { message, field } = errorData(e)
      problem.value = { field, message: message ?? 'Could not resolve the SEO preview.' }
    }
  }
  finally {
    if (mine === seq)
      resolving.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([seo, () => props.post, () => props.postId], () => {
  clearTimeout(timer)
  timer = setTimeout(resolve, 600)
}, { deep: true, immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

function errorOf(field: string) {
  return problem.value?.field === field ? problem.value.message : ''
}

const advanced = ['noarchive', 'nosnippet', 'noimageindex']
function toggleAdvanced(value: string, on: boolean) {
  const cur = new Set(seo.value.robots_advanced ?? [])
  if (on)
    cur.add(value)
  else
    cur.delete(value)
  set('robots_advanced', cur.size ? [...cur] : null)
}

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
</script>

<template>
  <div class="grid gap-4">
    <div class="flex items-center justify-between gap-2">
      <span class="font-display text-sm tracking-wide uppercase">SEO</span>
      <LoaderCircle v-if="resolving" class="size-4 animate-spin text-muted-foreground" aria-label="Updating preview" />
    </div>

    <FormError v-if="problem && !problem.field" :message="problem.message" />
    <Alert v-if="resolved?.unknown_vars.length">
      <TriangleAlert />
      <AlertTitle>Unknown variables</AlertTitle>
      <AlertDescription>
        {{ resolved.unknown_vars.map(v => `%%${v}%%`).join(', ') }} render as nothing.
      </AlertDescription>
    </Alert>

    <Tabs default-value="search" class="gap-4">
      <TabsList class="w-full">
        <TabsTrigger value="search">
          Search
        </TabsTrigger>
        <TabsTrigger value="social">
          Social
        </TabsTrigger>
        <TabsTrigger value="advanced">
          Advanced
        </TabsTrigger>
      </TabsList>

      <TabsContent value="search" class="grid gap-5">
        <SeoSnippet v-if="resolved" :title="resolved.title" :description="resolved.description" :url="resolved.url" />

        <div class="grid gap-2">
          <Label for="seo-title" :class="labelClass">SEO title</Label>
          <SeoVarInput id="seo-title" :model-value="seo.title" placeholder="Uses the title template" :invalid="!!errorOf('title')" @update:model-value="set('title', $event)" />
          <SeoLengthMeter :text="resolved?.title ?? ''" kind="title" />
          <FormError :message="errorOf('title')" />
        </div>
        <div class="grid gap-2">
          <Label for="seo-description" :class="labelClass">Meta description</Label>
          <SeoVarInput id="seo-description" multiline :model-value="seo.description" placeholder="Uses the description template" :invalid="!!errorOf('description')" @update:model-value="set('description', $event)" />
          <SeoLengthMeter :text="resolved?.description ?? ''" kind="description" />
          <FormError :message="errorOf('description')" />
        </div>
        <div class="grid gap-2">
          <Label for="seo-keyphrase" :class="labelClass">Focus keyphrase</Label>
          <Input id="seo-keyphrase" :model-value="seo.focus_keyphrase ?? ''" @update:model-value="text('focus_keyphrase', $event)" />
          <FormError :message="errorOf('focus_keyphrase')" />
        </div>

        <div v-if="resolved?.checks.length" class="grid gap-2">
          <span :class="labelClass">Checks</span>
          <SeoChecks :checks="resolved.checks" />
        </div>

        <div class="grid gap-2">
          <span :class="labelClass">Link suggestions</span>
          <p v-if="!resolved?.suggestions.length" class="text-xs text-muted-foreground">
            No related published posts yet. A title and keyphrase help.
          </p>
          <ul v-else class="grid gap-1">
            <li v-for="s in resolved.suggestions" :key="s.id" class="flex items-center gap-1 text-sm">
              <span class="min-w-0 flex-1 truncate" :title="s.url">{{ s.title }}</span>
              <Button variant="ghost" size="icon-xs" as="a" :href="s.url" target="_blank" rel="noopener" :aria-label="`Open ${s.title}`">
                <ExternalLink />
              </Button>
              <Button variant="ghost" size="icon-xs" type="button" :aria-label="`Copy link to ${s.title}`" @click="copyText(s.url)">
                <Copy />
              </Button>
            </li>
          </ul>
        </div>
      </TabsContent>

      <TabsContent value="social" class="grid gap-5">
        <SeoSocialCard v-if="resolved" :title="resolved.og.title" :description="resolved.og.description" :image="resolved.og.image" :url="resolved.url" />
        <div class="grid gap-2">
          <Label for="seo-og-title" :class="labelClass">Social title</Label>
          <SeoVarInput id="seo-og-title" :model-value="seo.og_title" placeholder="Uses the SEO title" @update:model-value="set('og_title', $event)" />
        </div>
        <div class="grid gap-2">
          <Label for="seo-og-description" :class="labelClass">Social description</Label>
          <SeoVarInput id="seo-og-description" multiline :model-value="seo.og_description" placeholder="Uses the meta description" @update:model-value="set('og_description', $event)" />
        </div>
        <div class="grid gap-2">
          <span :class="labelClass">Social image</span>
          <SeoMediaField :model-value="seo.og_image_media_id" placeholder="Featured image, else the first image, else the site default" @update:model-value="set('og_image_media_id', $event as number | null)" />
          <FormError :message="errorOf('og_image_media_id')" />
        </div>

        <div class="grid gap-3 border-t pt-4">
          <p class="text-xs text-muted-foreground">
            X uses the social values above unless you set its own.
          </p>
          <div class="grid gap-2">
            <Label for="seo-tw-title" :class="labelClass">X title</Label>
            <SeoVarInput id="seo-tw-title" :model-value="seo.twitter_title" :placeholder="resolved?.twitter.title" @update:model-value="set('twitter_title', $event)" />
          </div>
          <div class="grid gap-2">
            <Label for="seo-tw-description" :class="labelClass">X description</Label>
            <SeoVarInput id="seo-tw-description" multiline :model-value="seo.twitter_description" :placeholder="resolved?.twitter.description" @update:model-value="set('twitter_description', $event)" />
          </div>
          <div class="grid gap-2">
            <span :class="labelClass">X image</span>
            <SeoMediaField :model-value="seo.twitter_image_media_id" placeholder="Uses the social image" @update:model-value="set('twitter_image_media_id', $event as number | null)" />
            <FormError :message="errorOf('twitter_image_media_id')" />
          </div>
        </div>
      </TabsContent>

      <TabsContent value="advanced" class="grid gap-5">
        <div class="grid gap-2">
          <Label for="seo-canonical" :class="labelClass">Canonical URL</Label>
          <Input id="seo-canonical" :model-value="seo.canonical ?? ''" :placeholder="resolved?.url" :aria-invalid="!!errorOf('canonical')" @update:model-value="text('canonical', $event)" />
          <FormError :message="errorOf('canonical')" />
        </div>

        <div class="grid gap-3 rounded-md border p-3">
          <div class="flex items-center justify-between gap-4">
            <Label for="seo-noindex" class="text-sm">Hide from search results (noindex)</Label>
            <Switch id="seo-noindex" :model-value="seo.robots_noindex === true" @update:model-value="set('robots_noindex', $event ? true : null)" />
          </div>
          <div class="flex items-center justify-between gap-4">
            <Label for="seo-nofollow" class="text-sm">Don't follow links (nofollow)</Label>
            <Switch id="seo-nofollow" :model-value="seo.robots_nofollow === true" @update:model-value="set('robots_nofollow', $event ? true : null)" />
          </div>
          <div class="flex flex-wrap gap-x-4 gap-y-2">
            <label v-for="a in advanced" :key="a" class="flex items-center gap-2 font-mono text-xs">
              <Checkbox :model-value="seo.robots_advanced?.includes(a) ?? false" @update:model-value="toggleAdvanced(a, $event === true)" />
              {{ a }}
            </label>
          </div>
          <p class="font-mono text-[11px] break-all text-muted-foreground">
            {{ resolved?.robots }}
          </p>
          <FormError :message="errorOf('robots_advanced')" />
        </div>

        <div class="grid gap-2">
          <Label for="seo-breadcrumb" :class="labelClass">Breadcrumb title</Label>
          <Input id="seo-breadcrumb" :model-value="seo.breadcrumb_title ?? ''" :placeholder="post.title" @update:model-value="text('breadcrumb_title', $event)" />
          <p v-if="resolved?.breadcrumbs.length" class="truncate text-xs text-muted-foreground">
            {{ resolved.breadcrumbs.map(b => b.name).join(' › ') }}
          </p>
        </div>

        <div class="flex items-start justify-between gap-4 rounded-md border p-3">
          <div class="grid gap-1">
            <Label for="seo-cornerstone" class="text-sm">Cornerstone content</Label>
            <p class="text-xs text-muted-foreground">
              Mark the most important posts, the ones others should link to.
            </p>
          </div>
          <Switch id="seo-cornerstone" :model-value="seo.is_cornerstone" @update:model-value="set('is_cornerstone', $event)" />
        </div>

        <div class="grid gap-2">
          <span :class="labelClass">Videos</span>
          <SeoVideos v-if="postId !== 'new'" :post-id="postId" />
          <p v-else class="text-xs text-muted-foreground">
            Save the post to manage its videos.
          </p>
        </div>
      </TabsContent>
    </Tabs>
  </div>
</template>
