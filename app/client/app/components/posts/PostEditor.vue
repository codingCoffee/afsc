<script setup lang="ts">
import type { Conflict } from '@/components/editor/ConflictDialog.vue'
import type { RestoredRevision } from '@/components/editor/RevisionsSheet.vue'
import type { TagChip } from '@/components/editor/TagPicker.vue'
import type { TrashChoice } from '@/components/editor/TrashDialog.vue'
import type { JSONNode } from '~/editor/schema'
import type { AuthorRef, Autosave, ContentError, MediaItem, PMNode, PostDetail, PostInput, PostStatus, PostType, SeoFields, Templates, Term } from '~/types/api'
import { ArrowLeft, ExternalLink, Eye, History, ImageOff, Settings2, Trash2, Undo2 } from '@lucide/vue'
import { createReusableTemplate, useDebounceFn, useMediaQuery } from '@vueuse/core'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import AuthorPicker from '@/components/editor/AuthorPicker.vue'
import CategoryPicker from '@/components/editor/CategoryPicker.vue'
import ConflictDialog from '@/components/editor/ConflictDialog.vue'
import DateTimePicker from '@/components/editor/DateTimePicker.vue'
import Outline from '@/components/editor/Outline.vue'
import ParentPicker from '@/components/editor/ParentPicker.vue'
import RevisionsSheet from '@/components/editor/RevisionsSheet.vue'
import RichEditor from '@/components/editor/RichEditor.vue'
import TagPicker from '@/components/editor/TagPicker.vue'
import TrashDialog from '@/components/editor/TrashDialog.vue'
import MediaPicker from '@/components/media/MediaPicker.vue'
import SeoPanel from '@/components/seo/SeoPanel.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { API } from '~/composables/useAuth'
import { emptyDoc, normalizeDoc } from '~/editor/schema'

const props = defineProps<{ type: PostType }>()

interface Form {
  title: string
  slug: string
  status: PostStatus
  published_at: string | null
  excerpt: string
  parent_id: number | null
  template: string | null
  featured_media_id: number | null
  authors: AuthorRef[]
  category_ids: number[]
  primary_category_id: number | null
  tags: TagChip[]
  comment_status: 'open' | 'closed'
  sticky: boolean
  menu_order: number
  seo: SeoFields
}

const AUTOSAVE_MS = 10_000
const route = useRoute()
const router = useRouter()
const { authFetch, can } = useAuth()
const base = props.type === 'post' ? '/posts' : '/pages'
const noun = props.type === 'post' ? 'post' : 'page'

const blankSeo = (): SeoFields => ({
  title: null,
  description: null,
  canonical: null,
  robots_noindex: null,
  robots_nofollow: null,
  robots_advanced: null,
  og_title: null,
  og_description: null,
  og_image_media_id: null,
  twitter_title: null,
  twitter_description: null,
  twitter_image_media_id: null,
  focus_keyphrase: null,
  breadcrumb_title: null,
  is_cornerstone: false,
})

const blankForm = (): Form => ({
  title: '',
  slug: '',
  status: 'draft',
  published_at: null,
  excerpt: '',
  parent_id: null,
  template: null,
  featured_media_id: null,
  authors: [],
  category_ids: [],
  primary_category_id: null,
  tags: [],
  comment_status: props.type === 'post' ? 'open' : 'closed',
  sticky: false,
  menu_order: 0,
  seo: blankSeo(),
})

const id = ref<number | null>(null)
const detail = ref<PostDetail | null>(null)
const form = reactive<Form>(blankForm())
const featured = ref<MediaItem | null>(null)
const initialContent = ref<JSONNode>(emptyDoc())
const content = ref<JSONNode>(emptyDoc())
const editorKey = ref(0)
const loaded = ref(false)
const loadError = ref('')
const rich = ref<InstanceType<typeof RichEditor> | null>(null)
const words = ref(0)

const templates = ref<string[]>([])
const allowedClasses = ref<string[]>([])
authFetch<Partial<Templates>>(`${API}/templates`).then((res) => {
  const list = res?.[props.type]
  templates.value = Array.isArray(list) ? list : []
  allowedClasses.value = Array.isArray(res?.allowed_classes) ? res.allowed_classes : []
}).catch(() => {})

// dirty: unsaved changes; pendingAutosave: changes since the last autosave
const dirty = ref(false)
let pendingAutosave = false
let quiet = false
let autosaveTimer: ReturnType<typeof setTimeout> | undefined
const autosavedAt = ref<string | null>(null)

function touch() {
  if (quiet)
    return
  dirty.value = true
  pendingAutosave = true
  clearTimeout(autosaveTimer)
  if (id.value != null)
    autosaveTimer = setTimeout(() => pendingAutosave && autosave().catch(() => {}), AUTOSAVE_MS)
}

async function settle() {
  quiet = true
  await nextTick()
  quiet = false
  dirty.value = false
  pendingAutosave = false
  clearTimeout(autosaveTimer)
}

watch(form, touch, { deep: true })

const syncContent = useDebounceFn(() => {
  const ed = rich.value?.editor
  if (!ed)
    return
  content.value = normalizeDoc(ed.getJSON() as JSONNode)
  words.value = ed.storage.characterCount.words()
}, 800)

watch(rich, () => syncContent())

function onEditorChange() {
  touch()
  syncContent()
}

function currentContent(): PMNode {
  const ed = rich.value?.editor
  return (ed ? normalizeDoc(ed.getJSON() as JSONNode) : content.value) as PMNode
}

async function loadTags(ids: number[]) {
  const terms = await Promise.all(ids.map(t => authFetch<Term>(`${API}/terms/${t}`).catch(() => null)))
  return ids.map((t, i) => ({ id: t, name: terms[i]?.name ?? `#${t}` }))
}

function fill(d: PostDetail, tags: TagChip[]) {
  detail.value = d
  id.value = d.id
  Object.assign(form, {
    title: d.title,
    slug: d.slug,
    status: d.status,
    published_at: d.published_at,
    excerpt: d.excerpt,
    parent_id: d.parent_id,
    template: d.template,
    featured_media_id: d.featured_media_id,
    authors: d.authors,
    category_ids: d.category_ids,
    primary_category_id: d.primary_category_id,
    tags,
    comment_status: d.comment_status,
    sticky: d.sticky,
    menu_order: d.menu_order,
    seo: { ...blankSeo(), ...d.seo },
  })
  featured.value = d.featured_media
}

const autosavePrompt = ref<Autosave | null>(null)

async function load() {
  loaded.value = false
  loadError.value = ''
  contentErrors.value = []
  const param = String(route.params.id)
  try {
    if (param === 'new') {
      id.value = null
      detail.value = null
      Object.assign(form, blankForm())
      featured.value = null
      autosavePrompt.value = null
      initialContent.value = emptyDoc()
    }
    else {
      const d = await authFetch<PostDetail>(`${API}/posts/${param}`)
      if (d.type !== props.type)
        return navigateTo(`${d.type === 'post' ? '/posts' : '/pages'}/${d.id}`, { replace: true })
      fill(d, await loadTags(d.tag_ids))
      autosavePrompt.value = d.autosave
      initialContent.value = d.content as JSONNode
    }
    content.value = initialContent.value
    editorKey.value++
    loaded.value = true
    await settle()
  }
  catch (e) {
    loadError.value = errorData(e).message ?? `Could not load this ${noun}.`
  }
}

function setEditorContent(doc: PMNode) {
  rich.value?.editor?.commands.setContent(doc)
  content.value = doc as JSONNode
}

function restoreAutosave() {
  const a = autosavePrompt.value
  if (!a)
    return
  form.title = a.title
  form.excerpt = a.excerpt
  if (a.seo)
    form.seo = { ...blankSeo(), ...a.seo }
  setEditorContent(a.content)
  autosavePrompt.value = null
  touch()
}

function payload(status: PostStatus): PostInput {
  return {
    type: props.type,
    title: form.title,
    slug: form.slug,
    status,
    published_at: form.published_at,
    excerpt: form.excerpt,
    content: currentContent(),
    parent_id: props.type === 'page' ? form.parent_id : null,
    template: form.template,
    featured_media_id: form.featured_media_id,
    author_ids: form.authors.map(a => a.id),
    category_ids: props.type === 'post' ? form.category_ids : [],
    primary_category_id: props.type === 'post' ? form.primary_category_id : null,
    tag_ids: props.type === 'post' ? form.tags.map(t => t.id) : [],
    comment_status: form.comment_status,
    sticky: props.type === 'post' && form.sticky,
    menu_order: form.menu_order,
    seo: form.seo,
  }
}

async function autosave(): Promise<number | null> {
  if (id.value == null)
    return null
  clearTimeout(autosaveTimer)
  pendingAutosave = false
  const res = await authFetch<{ revision_id: number, saved_at: string }>(`${API}/posts/${id.value}/autosave`, {
    method: 'POST',
    body: { title: form.title, excerpt: form.excerpt, content: currentContent(), seo: form.seo },
  })
  autosavedAt.value = res.saved_at
  return res.revision_id
}

const saving = ref(false)
const contentErrors = ref<ContentError[]>([])
const fieldErrors = ref<Record<string, string>>({})
const conflict = ref<Conflict | null>(null)
const conflictOpen = ref(false)
let lastStatus: PostStatus = 'draft'

const canPublish = computed(() => detail.value?.can_publish ?? (props.type === 'page' ? can('pages.edit') : can('posts.publish_own')))
const storedStatus = computed(() => detail.value?.status ?? 'draft')
const isLive = computed(() => ['published', 'private', 'scheduled'].includes(storedStatus.value))
const future = computed(() => !!form.published_at && new Date(form.published_at).getTime() > Date.now())

async function save(status: PostStatus, force = false) {
  if (saving.value)
    return
  saving.value = true
  lastStatus = status
  contentErrors.value = []
  fieldErrors.value = {}
  const wasNew = id.value == null
  const before = storedStatus.value
  try {
    const body = payload(status)
    const d = wasNew
      ? await authFetch<PostDetail>(`${API}/posts`, { method: 'POST', body })
      : await authFetch<PostDetail>(`${API}/posts/${id.value}`, { method: 'PUT', body: { ...body, version: detail.value!.version }, query: force ? { force: 1 } : undefined })
    fill(d, form.tags)
    autosavePrompt.value = null
    conflictOpen.value = false
    await settle()
    if (wasNew)
      await router.replace(`${base}/${d.id}`)
    const changed = d.status !== before
    toast.success(changed && d.status === 'scheduled' ? 'Scheduled.' : changed && d.status === 'published' ? 'Published.' : changed && d.status === 'pending' ? 'Submitted for review.' : 'Saved.')
  }
  catch (e) {
    const data = errorData<{ errors: ContentError[] } & Conflict>(e)
    if (statusOf(e) === 409 && data.code === 'conflict') {
      conflict.value = { current_version: data.current_version ?? 0, modified_by: data.modified_by ?? null, modified_at: data.modified_at ?? null }
      conflictOpen.value = true
    }
    else if (data.code === 'invalid_content' && data.errors) {
      contentErrors.value = data.errors
      toast.error('The content has problems; see the list above the editor.')
    }
    else if (data.field) {
      fieldErrors.value = { [data.field]: data.message ?? 'Invalid value.' }
      toast.error(data.message ?? 'Check the highlighted field.')
    }
    else {
      toast.error(data.message ?? 'Could not save.')
    }
  }
  finally {
    saving.value = false
  }
}
const quickSaveStatus = computed<PostStatus>(() => isLive.value ? form.status : form.status === 'pending' ? 'pending' : 'draft')

function onKey(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    if (loaded.value && storedStatus.value !== 'trash')
      save(quickSaveStatus.value)
  }
}

function onBeforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) {
    e.preventDefault()
    e.returnValue = ''
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKey)
  window.addEventListener('beforeunload', onBeforeUnload)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('beforeunload', onBeforeUnload)
  clearTimeout(autosaveTimer)
})
onBeforeRouteLeave(() => {
  if (dirty.value && !window.confirm('You have unsaved changes. Leave anyway?'))
    return false
})

async function preview() {
  if (id.value == null)
    return
  const w = window.open('', '_blank')
  if (w)
    w.opener = null
  try {
    const revision = await autosave()
    const res = await authFetch<{ url: string }>(`${API}/posts/${id.value}/preview`, { method: 'POST', body: { revision_id: revision ?? undefined } })
    if (w)
      w.location.href = res.url
    else
      window.open(res.url, '_blank', 'noopener')
  }
  catch (e) {
    w?.close()
    toast.error(errorData(e).message ?? 'Could not create a preview.')
  }
}

const trashOpen = ref(false)
async function trash(choice: TrashChoice) {
  try {
    const d = await authFetch<PostDetail>(`${API}/posts/${id.value}/trash`, { method: 'POST', body: storedStatus.value === 'published' ? choice : {} })
    fill(d, form.tags)
    await settle()
    toast.success('Moved to trash.')
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not move it to the trash.')
  }
}

async function restoreFromTrash() {
  try {
    const d = await authFetch<PostDetail>(`${API}/posts/${id.value}/restore`, { method: 'POST' })
    fill(d, form.tags)
    await settle()
    toast.success('Restored as a draft.')
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not restore it.')
  }
}

const revisionsOpen = ref(false)
function onRestoreRevision(r: RestoredRevision) {
  if (r.title != null)
    form.title = r.title
  if (r.excerpt != null)
    form.excerpt = r.excerpt
  if (r.seo)
    form.seo = { ...blankSeo(), ...r.seo }
  setEditorContent(r.content)
  touch()
}

const slugHint = ref<string | null>(null)
async function checkSlug() {
  slugHint.value = null
  if (!form.slug)
    return
  try {
    const res = await authFetch<{ available: boolean, suggestion: string }>(`${API}/posts/slug-check`, {
      query: { type: props.type, slug: form.slug, parent_id: form.parent_id ?? undefined, exclude: id.value ?? undefined },
    })
    if (!res.available)
      slugHint.value = res.suggestion
  }
  catch {}
}

const featuredOpen = ref(false)
function onFeatured(items: MediaItem[]) {
  const m = items[0]
  if (!m)
    return
  form.featured_media_id = m.id
  featured.value = m
}

const statusOptions = computed<PostStatus[]>(() => {
  const list: PostStatus[] = canPublish.value ? ['draft', 'pending', 'published', 'private'] : ['draft', 'pending']
  return list.includes(form.status) ? list : [...list, form.status]
})

const seoPost = computed(() => ({
  type: props.type,
  title: form.title,
  slug: form.slug,
  excerpt: form.excerpt,
  content: content.value as PMNode,
  featured_media_id: form.featured_media_id,
  category_ids: form.category_ids,
  primary_category_id: form.primary_category_id,
  published_at: form.published_at,
  parent_id: form.parent_id,
}))

const isDesktop = useMediaQuery('(min-width: 1024px)')
const settingsOpen = ref(false)
const [DefineSidebar, ReuseSidebar] = createReusableTemplate()
const fmt = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' })
const label = 'font-mono text-[10px] tracking-widest text-muted-foreground uppercase'

watch(() => route.params.id, (p) => {
  if (String(p) !== String(id.value ?? 'new'))
    load()
}, { immediate: true })

useHead({ title: computed(() => `${form.title || (id.value ? 'Untitled' : `New ${noun}`)} · AFSC`) })
</script>

<template>
  <DefineSidebar>
    <div class="grid gap-5 text-sm">
      <section class="grid gap-2">
        <template v-if="isLive">
          <Label for="post-status" :class="label">Status</Label>
          <NativeSelect id="post-status" v-model="form.status" class="w-full">
            <NativeSelectOption v-for="s in statusOptions" :key="s" :value="s">
              {{ s === 'private' ? 'private (logged-in only)' : s }}
            </NativeSelectOption>
          </NativeSelect>
        </template>
        <Label :class="label">Publish date</Label>
        <DateTimePicker v-model="form.published_at" />
        <p v-if="future" class="text-xs text-muted-foreground">
          Publishing schedules it for this time.
        </p>
      </section>

      <section class="grid gap-2">
        <div :class="label">
          Authors
        </div>
        <AuthorPicker v-model="form.authors" />
      </section>

      <template v-if="type === 'post'">
        <section class="grid gap-2">
          <div :class="label">
            Categories
          </div>
          <CategoryPicker v-model:ids="form.category_ids" v-model:primary="form.primary_category_id" />
        </section>
        <section class="grid gap-2">
          <div :class="label">
            Tags
          </div>
          <TagPicker v-model="form.tags" />
        </section>
      </template>

      <section class="grid gap-2">
        <div :class="label">
          Featured image
        </div>
        <div v-if="featured" class="overflow-hidden rounded-md border">
          <img v-if="featured.thumb_url || featured.url" :src="featured.thumb_url ?? featured.url" :alt="featured.alt" class="aspect-video w-full object-cover">
          <ImageOff v-else class="m-4 size-6 text-muted-foreground" />
        </div>
        <div class="flex gap-2">
          <Button variant="outline" size="sm" @click="featuredOpen = true">
            {{ featured ? 'Replace' : 'Choose image' }}
          </Button>
          <Button v-if="featured" variant="ghost" size="sm" @click="form.featured_media_id = null; featured = null">
            Remove
          </Button>
        </div>
      </section>

      <section class="grid gap-2">
        <Label for="post-excerpt" :class="label">Excerpt</Label>
        <Textarea id="post-excerpt" v-model="form.excerpt" rows="3" />
      </section>

      <section class="grid gap-2">
        <Label for="post-slug" :class="label">Slug</Label>
        <Input id="post-slug" v-model="form.slug" placeholder="from the title" autocapitalize="none" spellcheck="false" :aria-invalid="!!fieldErrors.slug" @blur="checkSlug" />
        <FormError :message="fieldErrors.slug" />
        <p v-if="slugHint" class="text-xs text-muted-foreground">
          Taken. <button type="button" class="underline" @click="form.slug = slugHint!; slugHint = null">Use {{ slugHint }}</button>
        </p>
        <a v-if="detail?.url" :href="detail.url" target="_blank" rel="noopener" class="inline-flex items-center gap-1 truncate text-xs text-muted-foreground hover:underline">
          <ExternalLink class="size-3" />
          {{ detail.url }}
        </a>
      </section>

      <section v-if="type === 'page'" class="grid gap-2">
        <div :class="label">
          Parent page
        </div>
        <ParentPicker v-model="form.parent_id" :exclude="id" />
        <Label for="post-order" :class="label">Menu order</Label>
        <Input id="post-order" v-model.number="form.menu_order" type="number" class="w-28" />
      </section>

      <section class="grid gap-2">
        <Label for="post-template" :class="label">Template</Label>
        <NativeSelect id="post-template" :model-value="form.template ?? ''" class="w-full" @update:model-value="form.template = ($event as string) || null">
          <NativeSelectOption value="">
            Default
          </NativeSelectOption>
          <NativeSelectOption v-for="t in templates" :key="t" :value="t">
            {{ t }}
          </NativeSelectOption>
          <NativeSelectOption v-if="form.template && !templates.includes(form.template)" :value="form.template">
            {{ form.template }} (missing)
          </NativeSelectOption>
        </NativeSelect>
      </section>

      <section class="grid gap-3">
        <Label class="flex items-center justify-between gap-2 font-normal">
          Allow comments
          <Switch :model-value="form.comment_status === 'open'" @update:model-value="form.comment_status = $event ? 'open' : 'closed'" />
        </Label>
        <Label v-if="type === 'post'" class="flex items-center justify-between gap-2 font-normal">
          Sticky on the home page
          <Switch v-model="form.sticky" />
        </Label>
      </section>

      <section class="grid gap-2">
        <div :class="label">
          Outline
        </div>
        <Outline v-if="rich?.editor" :editor="rich.editor" />
      </section>

      <section v-if="id != null" class="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" @click="revisionsOpen = true">
          <History />
          Revisions
        </Button>
        <Button v-if="storedStatus !== 'trash'" variant="ghost" size="sm" class="text-destructive" @click="trashOpen = true">
          <Trash2 />
          Move to trash
        </Button>
      </section>

      <Separator />
      <SeoPanel v-model:seo="form.seo" :post="seoPost" :post-id="id ?? 'new'" />
    </div>
  </DefineSidebar>

  <div class="grid gap-4">
    <div class="flex flex-wrap items-center gap-2">
      <Button variant="ghost" size="sm" as-child>
        <NuxtLink :to="base">
          <ArrowLeft />
          <span class="hidden sm:inline">{{ type === 'post' ? 'Posts' : 'Pages' }}</span>
        </NuxtLink>
      </Button>
      <Badge variant="outline" class="rounded-sm font-mono text-[10px] uppercase">
        {{ id == null ? 'new' : storedStatus }}
      </Badge>
      <span class="hidden font-mono text-[11px] text-muted-foreground sm:inline">
        {{ dirty ? 'Unsaved changes' : 'All changes saved' }}{{ autosavedAt ? ` · autosaved ${fmt.format(new Date(autosavedAt))}` : '' }}
      </span>
      <div class="ml-auto flex flex-wrap items-center gap-2">
        <Button v-if="!isDesktop" variant="outline" size="sm" aria-label="Post settings" @click="settingsOpen = true">
          <Settings2 />
        </Button>
        <Button v-if="id != null && storedStatus !== 'trash'" variant="outline" size="sm" @click="preview">
          <Eye />
          <span class="hidden sm:inline">Preview</span>
        </Button>
        <template v-if="storedStatus === 'trash'">
          <Button size="sm" @click="restoreFromTrash">
            <Undo2 />
            Restore
          </Button>
        </template>
        <template v-else-if="isLive">
          <Button size="sm" :disabled="saving" @click="save(form.status)">
            {{ saving ? 'Saving…' : 'Update' }}
          </Button>
        </template>
        <template v-else>
          <Button variant="outline" size="sm" :disabled="saving" @click="save(form.status === 'pending' ? 'pending' : 'draft')">
            {{ form.status === 'pending' ? 'Save' : 'Save draft' }}
          </Button>
          <Button v-if="canPublish" size="sm" :disabled="saving" @click="save('published')">
            {{ future ? 'Schedule' : 'Publish' }}
          </Button>
          <Button v-else-if="storedStatus !== 'pending'" size="sm" :disabled="saving" @click="save('pending')">
            Submit for review
          </Button>
        </template>
      </div>
    </div>

    <FormError :message="loadError" />

    <Alert v-if="storedStatus === 'trash'">
      <AlertTitle>This {{ noun }} is in the trash</AlertTitle>
      <AlertDescription>Restore it to edit or publish it again. It comes back as a draft.</AlertDescription>
    </Alert>

    <Alert v-if="autosavePrompt">
      <AlertTitle>There is a newer autosave</AlertTitle>
      <AlertDescription class="flex flex-wrap items-center gap-2">
        From {{ new Date(autosavePrompt.saved_at).toLocaleString() }}. Restore it into the editor?
        <Button size="sm" variant="outline" @click="restoreAutosave">
          Restore
        </Button>
        <Button size="sm" variant="ghost" @click="autosavePrompt = null">
          Dismiss
        </Button>
      </AlertDescription>
    </Alert>

    <Alert v-if="contentErrors.length" variant="destructive">
      <AlertTitle>The server rejected the content</AlertTitle>
      <AlertDescription>
        <ul class="grid gap-1 font-mono text-xs">
          <li v-for="(err, i) in contentErrors" :key="i">
            <span class="font-semibold">{{ err.path || 'doc' }}</span>: {{ err.message }}
          </li>
        </ul>
      </AlertDescription>
    </Alert>

    <div v-if="loaded" class="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div class="grid min-w-0 gap-3">
        <Input
          v-model="form.title"
          placeholder="Title"
          aria-label="Title"
          class="h-auto border-0 px-0 text-2xl font-semibold shadow-none focus-visible:ring-0 sm:text-3xl"
          :aria-invalid="!!fieldErrors.title"
        />
        <FormError :message="fieldErrors.title" />
        <RichEditor :key="editorKey" ref="rich" :content="initialContent" :allowed-classes="allowedClasses" @change="onEditorChange" />
        <p class="text-right font-mono text-[11px] text-muted-foreground">
          {{ words }} words
        </p>
      </div>
      <aside v-if="isDesktop" class="sticky top-20 max-h-[calc(100vh-6rem)] overflow-y-auto pr-1">
        <ReuseSidebar />
      </aside>
    </div>

    <Sheet v-if="!isDesktop" v-model:open="settingsOpen">
      <SheetContent class="w-full overflow-y-auto sm:max-w-md">
        <SheetHeader>
          <SheetTitle>{{ type === 'post' ? 'Post' : 'Page' }} settings</SheetTitle>
          <SheetDescription class="sr-only">
            Status, authors, terms and other settings.
          </SheetDescription>
        </SheetHeader>
        <div class="px-4 pb-6">
          <ReuseSidebar />
        </div>
      </SheetContent>
    </Sheet>

    <MediaPicker v-model:open="featuredOpen" accept="image" @select="onFeatured" />
    <ConflictDialog v-model:open="conflictOpen" :conflict="conflict" :can-overwrite="!!detail?.can_overwrite" @reload="conflictOpen = false; load()" @overwrite="save(lastStatus, true)" />
    <TrashDialog v-model:open="trashOpen" :published="storedStatus === 'published'" @confirm="trash" />
    <RevisionsSheet v-if="id != null" v-model:open="revisionsOpen" :post-id="id" @restore="onRestoreRevision" />
  </div>
</template>
