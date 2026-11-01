<script setup lang="ts">
import type { SettingsResponse } from '@/types/api'
import { KeyRound } from '@lucide/vue'
import { computed, ref } from 'vue'
import { toast } from 'vue-sonner'
import SeoMediaField from '@/components/seo/SeoMediaField.vue'
import SeoVarInput from '@/components/seo/SeoVarInput.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { TagsInput, TagsInputInput, TagsInputItem, TagsInputItemDelete, TagsInputItemText } from '@/components/ui/tags-input'
import { Textarea } from '@/components/ui/textarea'
import { API, errorData } from '@/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'settings.manage' })
useHead({ title: 'SEO settings · AFSC' })

// mirrors settings.SEO in internal/settings; the server rejects unknown keys
interface SeoSettings {
  separator: string
  title_templates: Record<string, string>
  description_templates: Record<string, string>
  knowledge_graph: { type: 'organization' | 'person', name: string, logo_media: number | string | null }
  social_profiles: string[] | null
  twitter_site: string
  default_image_media: number | string | null
  tag_noindex: boolean
  auto_description: boolean
  robots_txt: string
  llms_txt: { mode: 'off' | 'auto' | 'custom', body: string }
  sitemaps: { page_size: number }
  indexnow: { enabled: boolean, key: string }
  not_found: { noise_patterns: string[] | null, retention_days: number }
}

const types: { key: string, label: string }[] = [
  { key: 'post', label: 'Posts' },
  { key: 'page', label: 'Pages' },
  { key: 'category', label: 'Categories' },
  { key: 'tag', label: 'Tags' },
  { key: 'author', label: 'Authors' },
  { key: 'home', label: 'Home' },
  { key: 'date', label: 'Date archives' },
  { key: 'search', label: 'Search results' },
  { key: '404', label: '404 page' },
]
const separators = ['-', '–', '—', '|', '·', '•', '»', '~', ':']

const { authFetch } = useAuth()
const form = ref<SeoSettings | null>(null)
const saved = ref('')
const loadError = ref('')
const error = ref<{ field: string, message: string } | null>(null)
const saving = ref(false)
const dirty = computed(() => !!form.value && JSON.stringify(form.value) !== saved.value)

function adopt(v: unknown) {
  const s = JSON.parse(JSON.stringify(v)) as SeoSettings
  s.title_templates ??= {}
  s.description_templates ??= {}
  form.value = s
  saved.value = JSON.stringify(s)
}

async function load() {
  loadError.value = ''
  try {
    adopt((await authFetch<SettingsResponse>(`${API}/settings`)).values.seo)
  }
  catch (e) {
    loadError.value = errorData(e).message ?? 'Could not load settings.'
  }
}
load()

// empty templates are dropped, so the built-in default applies
function clean(s: SeoSettings): SeoSettings {
  const prune = (m: Record<string, string>) => Object.fromEntries(Object.entries(m).filter(([, v]) => v?.trim()))
  return { ...s, title_templates: prune(s.title_templates), description_templates: prune(s.description_templates) }
}

async function save() {
  if (!form.value)
    return
  saving.value = true
  error.value = null
  try {
    const res = await authFetch<{ group: string, value: unknown }>(`${API}/settings/seo`, { method: 'PUT', body: clean(form.value) })
    adopt(res.value)
    toast.success('SEO settings saved. The page cache was cleared.')
  }
  catch (e) {
    const { message, field } = errorData(e)
    error.value = { field: field ?? '', message: message ?? 'Could not save.' }
  }
  finally {
    saving.value = false
  }
}

function errorFor(key: string) {
  const e = error.value
  return e && (e.field === key || e.field.startsWith(`${key}.`)) ? e.message : ''
}

function generateKey() {
  const b = new Uint8Array(16)
  crypto.getRandomValues(b)
  form.value!.indexnow.key = [...b].map(x => x.toString(16).padStart(2, '0')).join('')
}

const noise = computed({
  get: () => (form.value?.not_found.noise_patterns ?? []).join('\n'),
  set: (v: string) => {
    form.value!.not_found.noise_patterns = v.split('\n').map(s => s.trim()).filter(Boolean)
  },
})

function num(v: string | number) {
  return v === '' ? 0 : Number(v)
}

const labelClass = 'font-mono text-[11px] tracking-widest text-muted-foreground uppercase'
const sectionClass = 'grid gap-4 border-t pt-6'
const headingClass = 'font-display text-sm tracking-wide uppercase'
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="SEO settings" description="Titles, social data, sitemaps and crawler files for the whole site.">
      <template #actions>
        <Button size="sm" :disabled="!dirty || saving" @click="save">
          {{ saving ? 'Saving' : 'Save' }}
        </Button>
      </template>
    </PageHeader>

    <FormError :message="loadError" />
    <div v-if="!form && !loadError" class="grid gap-3">
      <Skeleton v-for="i in 4" :key="i" class="h-10 w-full" />
    </div>

    <form v-if="form" class="grid max-w-3xl gap-6" @submit.prevent="save">
      <section class="grid gap-4">
        <h2 :class="headingClass">
          Titles and descriptions
        </h2>
        <div class="grid gap-2">
          <Label for="s-sep" :class="labelClass">Separator</Label>
          <div class="flex flex-wrap items-center gap-1.5">
            <Input id="s-sep" v-model="form.separator" class="w-16 text-center" :aria-invalid="!!errorFor('separator')" />
            <Button
              v-for="s in separators"
              :key="s"
              type="button"
              size="icon-sm"
              :variant="form.separator === s ? 'default' : 'outline'"
              :aria-label="`Use ${s}`"
              @click="form.separator = s"
            >
              {{ s }}
            </Button>
          </div>
          <FormError :message="errorFor('separator')" />
        </div>
        <div v-for="t in types" :key="t.key" class="grid gap-3 rounded-md border p-3">
          <span class="text-sm font-medium">{{ t.label }}</span>
          <div class="grid gap-1.5">
            <Label :for="`s-title-${t.key}`" :class="labelClass">Title</Label>
            <SeoVarInput
              :id="`s-title-${t.key}`"
              :model-value="form.title_templates[t.key] ?? null"
              placeholder="Built-in default"
              @update:model-value="form.title_templates[t.key] = $event ?? ''"
            />
          </div>
          <div class="grid gap-1.5">
            <Label :for="`s-desc-${t.key}`" :class="labelClass">Description</Label>
            <SeoVarInput
              :id="`s-desc-${t.key}`"
              :model-value="form.description_templates[t.key] ?? null"
              placeholder="None"
              @update:model-value="form.description_templates[t.key] = $event ?? ''"
            />
          </div>
        </div>
        <FormError :message="errorFor('title_templates') || errorFor('description_templates')" />
        <div class="flex items-start justify-between gap-4 rounded-md border p-3">
          <div class="grid gap-1">
            <Label for="s-auto" class="text-sm">Fall back to the excerpt for descriptions</Label>
            <p class="text-xs text-muted-foreground">
              When off, pages without a description template or override have no meta description.
            </p>
          </div>
          <Switch id="s-auto" v-model="form.auto_description" />
        </div>
        <div class="flex items-start justify-between gap-4 rounded-md border p-3">
          <div class="grid gap-1">
            <Label for="s-tagnoindex" class="text-sm">Hide tag archives from search results</Label>
            <p class="text-xs text-muted-foreground">
              Adds noindex to tag pages and leaves them out of sitemaps, unless a tag overrides it.
            </p>
          </div>
          <Switch id="s-tagnoindex" v-model="form.tag_noindex" />
        </div>
      </section>

      <section :class="sectionClass">
        <h2 :class="headingClass">
          Knowledge graph and social
        </h2>
        <div class="grid gap-4 sm:grid-cols-2">
          <div class="grid gap-2">
            <Label for="s-kg-type" :class="labelClass">Site represents</Label>
            <NativeSelect id="s-kg-type" v-model="form.knowledge_graph.type" class="w-full">
              <NativeSelectOption value="organization">
                An organization
              </NativeSelectOption>
              <NativeSelectOption value="person">
                A person
              </NativeSelectOption>
            </NativeSelect>
          </div>
          <div class="grid gap-2">
            <Label for="s-kg-name" :class="labelClass">Name</Label>
            <Input id="s-kg-name" v-model="form.knowledge_graph.name" placeholder="Site title" />
          </div>
        </div>
        <div class="grid gap-2">
          <span :class="labelClass">{{ form.knowledge_graph.type === 'person' ? 'Photo' : 'Logo' }}</span>
          <SeoMediaField v-model="form.knowledge_graph.logo_media" />
          <FormError :message="errorFor('knowledge_graph')" />
        </div>
        <div class="grid gap-2">
          <Label for="s-social" :class="labelClass">Social profiles</Label>
          <TagsInput id="s-social" :model-value="form.social_profiles ?? []" @update:model-value="form.social_profiles = $event as string[]">
            <TagsInputItem v-for="p in form.social_profiles ?? []" :key="p" :value="p">
              <TagsInputItemText />
              <TagsInputItemDelete />
            </TagsInputItem>
            <TagsInputInput placeholder="https://… and Enter" />
          </TagsInput>
          <FormError :message="errorFor('social_profiles')" />
        </div>
        <div class="grid gap-2">
          <Label for="s-twitter" :class="labelClass">X account</Label>
          <Input id="s-twitter" v-model="form.twitter_site" placeholder="@handle" class="max-w-xs" />
        </div>
        <div class="grid gap-2">
          <span :class="labelClass">Default social image</span>
          <SeoMediaField v-model="form.default_image_media" placeholder="Used when a page has no image" />
          <FormError :message="errorFor('default_image_media')" />
        </div>
      </section>

      <section :class="sectionClass">
        <h2 :class="headingClass">
          Crawlers
        </h2>
        <div class="grid gap-2">
          <Label for="s-sitemap" :class="labelClass">URLs per sitemap file</Label>
          <Input
            id="s-sitemap"
            type="number"
            min="1"
            max="50000"
            inputmode="numeric"
            class="w-40"
            :model-value="form.sitemaps.page_size"
            :aria-invalid="!!errorFor('sitemaps')"
            @update:model-value="form.sitemaps.page_size = num($event)"
          />
          <FormError :message="errorFor('sitemaps')" />
        </div>
        <div class="grid gap-2">
          <Label for="s-robots" :class="labelClass">robots.txt</Label>
          <Textarea id="s-robots" v-model="form.robots_txt" rows="6" spellcheck="false" class="font-mono text-xs" placeholder="Empty allows everything. The sitemap index is always appended." />
          <FormError :message="errorFor('robots_txt')" />
        </div>
        <div class="grid gap-2">
          <Label for="s-llms" :class="labelClass">llms.txt</Label>
          <NativeSelect id="s-llms" v-model="form.llms_txt.mode" class="w-full sm:w-64">
            <NativeSelectOption value="off">
              Off
            </NativeSelectOption>
            <NativeSelectOption value="auto">
              Generated from the site
            </NativeSelectOption>
            <NativeSelectOption value="custom">
              Custom
            </NativeSelectOption>
          </NativeSelect>
          <Textarea
            v-if="form.llms_txt.mode === 'custom'"
            v-model="form.llms_txt.body"
            rows="8"
            spellcheck="false"
            class="font-mono text-xs"
            aria-label="llms.txt body"
            :aria-invalid="!!errorFor('llms_txt')"
          />
          <FormError :message="errorFor('llms_txt')" />
        </div>
        <div class="grid gap-3 rounded-md border p-3">
          <div class="flex items-start justify-between gap-4">
            <div class="grid gap-1">
              <Label for="s-indexnow" class="text-sm">IndexNow</Label>
              <p class="text-xs text-muted-foreground">
                Tells Bing, Yandex and others about published, changed and removed posts within a minute.
              </p>
            </div>
            <Switch id="s-indexnow" v-model="form.indexnow.enabled" />
          </div>
          <div class="flex flex-wrap gap-2">
            <Input v-model="form.indexnow.key" aria-label="IndexNow key" class="max-w-sm font-mono text-xs" placeholder="Key" :aria-invalid="!!errorFor('indexnow')" />
            <Button type="button" variant="outline" size="sm" @click="generateKey">
              <KeyRound />
              Generate key
            </Button>
          </div>
          <FormError :message="errorFor('indexnow')" />
        </div>
      </section>

      <section :class="sectionClass">
        <h2 :class="headingClass">
          404 log
        </h2>
        <div class="grid gap-2">
          <Label for="s-noise" :class="labelClass">Ignored paths</Label>
          <Textarea id="s-noise" v-model="noise" rows="6" spellcheck="false" class="font-mono text-xs" :aria-invalid="!!errorFor('not_found')" />
          <p class="text-xs text-muted-foreground">
            One pattern per line, matched against the whole path, ignoring case. * matches anything including /, ? one character.
          </p>
        </div>
        <div class="grid gap-2">
          <Label for="s-retention" :class="labelClass">Keep entries for (days)</Label>
          <Input
            id="s-retention"
            type="number"
            min="1"
            max="3650"
            inputmode="numeric"
            class="w-40"
            :model-value="form.not_found.retention_days"
            @update:model-value="form.not_found.retention_days = num($event)"
          />
        </div>
        <FormError :message="errorFor('not_found')" />
      </section>

      <FormError v-if="error" :message="error.message" />
      <div>
        <Button type="submit" :disabled="!dirty || saving">
          {{ saving ? 'Saving' : 'Save' }}
        </Button>
      </div>
    </form>
  </div>
</template>
