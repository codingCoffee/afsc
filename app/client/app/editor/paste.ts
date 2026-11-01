import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { schema } from './schema'

export interface EmbedAttrs { provider: string, url: string, externalId?: string }

type Matcher = (u: URL) => EmbedAttrs | null

// Each matcher yields a URL the Go validator accepts for that provider (validate.go embedURLPrefixes).
const matchers: Record<string, Matcher> = {
  youtube: (u) => {
    const host = u.hostname.replace(/^(www|m)\./, '')
    let id: string | null = null
    if (host === 'youtu.be')
      id = u.pathname.slice(1).split('/')[0] ?? null
    else if (host === 'youtube.com')
      id = u.pathname === '/watch' ? u.searchParams.get('v') : (u.pathname.match(/^\/(?:shorts|embed|live)\/([^/]+)/)?.[1] ?? null)
    if (!id || !/^[\w-]{11}$/.test(id))
      return null
    return { provider: 'youtube', url: `https://www.youtube.com/watch?v=${id}`, externalId: id }
  },
  vimeo: (u) => {
    const host = u.hostname.replace(/^www\./, '')
    const id = host === 'vimeo.com'
      ? u.pathname.match(/^\/(\d{1,20})(?:\/|$)/)?.[1]
      : host === 'player.vimeo.com' ? u.pathname.match(/^\/video\/(\d{1,20})/)?.[1] : undefined
    return id ? { provider: 'vimeo', url: `https://vimeo.com/${id}`, externalId: id } : null
  },
  x: (u) => {
    const host = u.hostname.replace(/^(www|mobile)\./, '')
    const m = u.pathname.match(/^\/(\w{1,15})\/status\/(\d{1,25})/)
    return (host === 'x.com' || host === 'twitter.com') && m ? { provider: 'x', url: `https://x.com/${m[1]}/status/${m[2]}` } : null
  },
  instagram: (u) => {
    const m = u.pathname.match(/^\/(p|reel|tv)\/([\w-]{1,64})/)
    return u.hostname.replace(/^www\./, '') === 'instagram.com' && m ? { provider: 'instagram', url: `https://www.instagram.com/${m[1]}/${m[2]}/` } : null
  },
  spotify: (u) => {
    const m = u.pathname.match(/^\/(?:intl-[a-z-]+\/)?(track|album|playlist|episode|show|artist)\/([A-Z0-9]{1,64})/i)
    return u.hostname === 'open.spotify.com' && m ? { provider: 'spotify', url: `https://open.spotify.com/${m[1]}/${m[2]}` } : null
  },
  soundcloud: (u) => {
    const path = u.pathname.replace(/\/+$/, '')
    return u.hostname.replace(/^(www|m)\./, '') === 'soundcloud.com' && /^\/[\w-]+\/[\w-]+/.test(path)
      ? { provider: 'soundcloud', url: `https://soundcloud.com${path}` }
      : null
  },
  googlemaps: (u) => {
    return u.hostname === 'www.google.com' && u.pathname === '/maps/embed' && u.search
      ? { provider: 'googlemaps', url: `https://www.google.com/maps/embed${u.search}` }
      : null
  },
}

// embedFromUrl maps a lone URL of an allowlisted provider to embed attrs, or null.
export function embedFromUrl(text: string): EmbedAttrs | null {
  const raw = text.trim()
  if (!raw || /\s/.test(raw) || !/^https?:\/\//i.test(raw))
    return null
  let u: URL
  try {
    u = new URL(raw.replace(/^http:/i, 'https:'))
  }
  catch {
    return null
  }
  for (const provider of schema.embed_providers) {
    const hit = matchers[provider]?.(u)
    if (hit)
      return hit
  }
  return null
}

export const EmbedPaste = Extension.create({
  name: 'embedPaste',
  priority: 1000,

  addProseMirrorPlugins() {
    return [new Plugin({
      key: new PluginKey('embedPaste'),
      props: {
        handlePaste: (view, event) => {
          const attrs = embedFromUrl(event.clipboardData?.getData('text/plain') ?? '')
          const type = view.state.schema.nodes.embed
          const sel = view.state.selection
          if (!attrs || !type || !sel.empty || sel.$from.parent.type.spec.code)
            return false
          view.dispatch(view.state.tr.replaceSelectionWith(type.create(attrs)).scrollIntoView())
          return true
        },
      },
    })]
  },
})
