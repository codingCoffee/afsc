<script setup lang="ts">
import type { EditorHost, MediaAccept } from '~/editor/host'
import type { JSONNode } from '~/editor/schema'
import type { MediaItem } from '~/types/api'
import { EditorContent, useEditor, VueNodeViewRenderer } from '@tiptap/vue-3'
import { provide, ref, watch } from 'vue'
import { API } from '~/composables/useAuth'
import MediaPicker from '@/components/media/MediaPicker.vue'
import { editorHostKey } from '~/editor/host'
import { buildExtensions } from '~/editor/kit'
import Toolbar from './Toolbar.vue'
import EmbedView from './views/EmbedView.vue'
import GalleryView from './views/GalleryView.vue'
import ImageView from './views/ImageView.vue'
import RawHtmlView from './views/RawHtmlView.vue'
import ReadMoreView from './views/ReadMoreView.vue'
import VideoView from './views/VideoView.vue'

const props = defineProps<{ content: JSONNode, allowedClasses: string[] }>()
const emit = defineEmits<{ change: [] }>()
const { authFetch, can } = useAuth()

const pickerOpen = ref(false)
const pickerAccept = ref<MediaAccept>('image')
const pickerMultiple = ref(false)
let resolvePick: ((items: MediaItem[]) => void) | null = null
const mediaCache = new Map<number, Promise<MediaItem | null>>()

const host: EditorHost = {
  pickMedia(accept, multiple = false) {
    resolvePick?.([])
    pickerAccept.value = accept
    pickerMultiple.value = multiple
    pickerOpen.value = true
    return new Promise((resolve) => {
      resolvePick = resolve
    })
  },
  media(id) {
    if (!mediaCache.has(id))
      mediaCache.set(id, authFetch<MediaItem>(`${API}/media/${id}`).catch(() => null))
    return mediaCache.get(id)!
  },
}
provide(editorHostKey, host)

function onPick(items: MediaItem[]) {
  for (const m of items)
    mediaCache.set(m.id, Promise.resolve(m))
  resolvePick?.(items)
  resolvePick = null
}

// the picker may close before or after emitting select, so settle a cancelled pick on the next tick
watch(pickerOpen, (open) => {
  if (!open)
    setTimeout(() => onPick([]), 0)
})

const linkOpen = ref(false)

const editor = useEditor({
  content: props.content,
  extensions: buildExtensions({
    allowedClasses: () => props.allowedClasses,
    canRawHtml: () => can('content.raw_html'),
    placeholder: 'Start writing…',
    nodeViews: {
      image: VueNodeViewRenderer(ImageView),
      embed: VueNodeViewRenderer(EmbedView),
      video: VueNodeViewRenderer(VideoView),
      gallery: VueNodeViewRenderer(GalleryView),
      rawHtml: VueNodeViewRenderer(RawHtmlView),
      readMore: VueNodeViewRenderer(ReadMoreView),
    },
  }),
  editorProps: {
    attributes: { 'class': 'afsc-prose min-h-[50vh] px-4 py-6 outline-none sm:px-8', 'aria-label': 'Content' },
    handleKeyDown: (_view, event) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        linkOpen.value = true
        return true
      }
      return false
    },
  },
  onUpdate: () => emit('change'),
})

defineExpose({ editor })
</script>

<template>
  <div class="rounded-md border">
    <div class="sticky top-14 z-20">
      <Toolbar v-if="editor" v-model:link-open="linkOpen" :editor="editor" :allowed-classes="allowedClasses" :can-raw-html="can('content.raw_html')" />
    </div>
    <EditorContent :editor="editor" />
    <MediaPicker v-model:open="pickerOpen" :accept="pickerAccept" :multiple="pickerMultiple" @select="onPick" />
  </div>
</template>

<style>
.afsc-prose { line-height: 1.7; font-size: 1rem; }
.afsc-prose > * + * { margin-top: 0.9em; }
.afsc-prose h1 { font-size: 2em; font-weight: 700; line-height: 1.2; }
.afsc-prose h2 { font-size: 1.5em; font-weight: 700; line-height: 1.25; }
.afsc-prose h3 { font-size: 1.25em; font-weight: 600; }
.afsc-prose h4, .afsc-prose h5, .afsc-prose h6 { font-weight: 600; }
.afsc-prose ul { list-style: disc; padding-left: 1.5em; }
.afsc-prose ol { list-style: decimal; padding-left: 1.5em; }
.afsc-prose a { color: var(--primary); text-decoration: underline; }
.afsc-prose blockquote { border-left: 3px solid var(--border); padding-left: 1em; color: var(--muted-foreground); }
.afsc-prose code { font-family: var(--font-mono, monospace); font-size: 0.9em; background: var(--muted); border-radius: 0.25rem; padding: 0.1em 0.3em; }
.afsc-prose pre { background: var(--muted); border-radius: 0.375rem; padding: 0.75em 1em; overflow-x: auto; }
.afsc-prose pre code { background: none; padding: 0; }
.afsc-prose hr { border-color: var(--border); margin: 1.5em 0; }
.afsc-prose table { border-collapse: collapse; width: 100%; table-layout: fixed; overflow: hidden; }
.afsc-prose td, .afsc-prose th { border: 1px solid var(--border); padding: 0.4em 0.6em; vertical-align: top; position: relative; }
.afsc-prose th { background: var(--muted); font-weight: 600; text-align: left; }
.afsc-prose .selectedCell::after { content: ''; position: absolute; inset: 0; background: color-mix(in oklab, var(--primary) 15%, transparent); pointer-events: none; }
.afsc-prose .tableWrapper { overflow-x: auto; }
.afsc-prose figure { border: 1px dashed var(--border); border-radius: 0.375rem; padding: 0.5em; }
.afsc-prose figcaption { font-size: 0.875em; color: var(--muted-foreground); text-align: center; }
.afsc-prose div[data-type='container'] { border: 1px dashed var(--border); border-radius: 0.375rem; padding: 0.75em; position: relative; }
.afsc-prose div[data-type='container']::before { content: attr(data-classes); position: absolute; top: -0.7em; left: 0.5em; padding: 0 0.3em; background: var(--background); font: 10px var(--font-mono, monospace); text-transform: uppercase; letter-spacing: 0.1em; color: var(--muted-foreground); }
.afsc-prose [data-type='details'], .afsc-prose details { border: 1px solid var(--border); border-radius: 0.375rem; padding: 0.5em 0.75em; }
.afsc-prose [data-type='details'] { display: flex; gap: 0.25rem; }
.afsc-prose [data-type='details'] > button { width: 1.25rem; }
.afsc-prose [data-type='details'] > div { flex: 1; }
.afsc-prose [data-type='details']:not(.is-open) [data-type='detailsContent'] { display: none; }
.afsc-prose summary { font-weight: 600; }
.afsc-prose p.is-editor-empty:first-child::before { content: attr(data-placeholder); float: left; height: 0; pointer-events: none; color: var(--muted-foreground); }
.afsc-prose .ProseMirror-selectednode { outline: none; }
.afsc-prose .afsc-gallery > * { margin: 0; }
</style>
