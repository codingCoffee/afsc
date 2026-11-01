import type { InjectionKey } from 'vue'
import type { MediaItem } from '~/types/api'

export type MediaAccept = 'image' | 'video' | 'any'

// What node views need from the page hosting the editor; provided by RichEditor.
export interface EditorHost {
  pickMedia: (accept: MediaAccept, multiple?: boolean) => Promise<MediaItem[]>
  media: (id: number) => Promise<MediaItem | null>
}

export const editorHostKey: InjectionKey<EditorHost> = Symbol('editorHost')
