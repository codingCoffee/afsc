<script setup lang="ts">
import type { UserRef } from '@/components/taxonomy/UserPicker.vue'
import type { Author, AuthorInput, MediaItem } from '@/types/api'
import { Image as ImageIcon, Link2Off, Plus, UserRound, X } from '@lucide/vue'
import { reactive, ref, watch } from 'vue'
import MediaPicker from '@/components/media/MediaPicker.vue'
import UserPicker from '@/components/taxonomy/UserPicker.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'

const props = defineProps<{ author: Author | null }>()
const emit = defineEmits<{ saved: [author: Author] }>()
const open = defineModel<boolean>('open', { default: false })

const { authFetch, can } = useAuth()
const form = reactive<AuthorInput>({ slug: '', display_name: '', bio: '', website: '', social: {}, avatar_media_id: null, user_id: null })
const social = ref<{ key: string, url: string }[]>([])
const avatarURL = ref<string | null>(null)
const userName = ref<string | null>(null)
const pickerOpen = ref(false)
const userOpen = ref(false)
const errors = ref<Record<string, string>>({})
const saving = ref(false)

watch(open, (v) => {
  if (!v)
    return
  const a = props.author
  Object.assign(form, {
    slug: a?.slug ?? '',
    display_name: a?.display_name ?? '',
    bio: a?.bio ?? '',
    website: a?.website ?? '',
    avatar_media_id: a?.avatar_media_id ?? null,
    user_id: a?.user_id ?? null,
  })
  social.value = Object.entries(a?.social ?? {}).map(([key, url]) => ({ key, url }))
  avatarURL.value = a?.avatar_url ?? null
  userName.value = a?.user_name ?? null
  errors.value = {}
})

function onAvatar(items: MediaItem[]) {
  const m = items[0]
  if (!m)
    return
  form.avatar_media_id = m.id
  avatarURL.value = m.thumb_url ?? m.url
}

function onUser(u: UserRef) {
  form.user_id = u.id
  userName.value = u.display_name || u.username
  userOpen.value = false
}

async function submit() {
  saving.value = true
  errors.value = {}
  form.social = Object.fromEntries(social.value.filter(s => s.key.trim() && s.url.trim()).map(s => [s.key.trim().toLowerCase(), s.url.trim()]))
  try {
    const a = props.author
    const saved = await authFetch<Author>(a ? `/admin/api/v1/authors/${a.id}` : '/admin/api/v1/authors', { method: a ? 'PUT' : 'POST', body: { ...form } })
    emit('saved', saved)
    open.value = false
  }
  catch (e) {
    const { message, field } = errorData(e)
    errors.value = { [field ?? 'form']: message ?? 'Could not save.' }
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full overflow-y-auto sm:max-w-lg">
      <SheetHeader>
        <SheetTitle>{{ author ? 'Edit author' : 'New author' }}</SheetTitle>
        <SheetDescription>Authors without a user account are guest authors. Changing the slug redirects the old archive URL.</SheetDescription>
      </SheetHeader>
      <form id="author-form" class="grid gap-4 px-4" @submit.prevent="submit">
        <div class="flex items-center gap-4">
          <div class="flex size-16 shrink-0 items-center justify-center overflow-hidden rounded-full border bg-muted/40">
            <img v-if="avatarURL" :src="avatarURL" alt="" class="size-full object-cover">
            <UserRound v-else class="size-6 text-muted-foreground" />
          </div>
          <div class="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" @click="pickerOpen = true">
              <ImageIcon /> {{ avatarURL ? 'Change avatar' : 'Choose avatar' }}
            </Button>
            <Button v-if="form.avatar_media_id" type="button" variant="ghost" size="sm" @click="form.avatar_media_id = null; avatarURL = null">
              Remove
            </Button>
          </div>
        </div>
        <FormError :message="errors.avatar_media_id" />

        <div class="grid gap-2">
          <Label for="au-name">Display name</Label>
          <Input id="au-name" v-model="form.display_name" required :aria-invalid="!!errors.display_name" />
          <FormError :message="errors.display_name" />
        </div>
        <div class="grid gap-2">
          <Label for="au-slug">Slug <span class="text-muted-foreground">(optional)</span></Label>
          <Input id="au-slug" v-model="form.slug" autocapitalize="none" spellcheck="false" class="font-mono" :aria-invalid="!!errors.slug" />
          <FormError :message="errors.slug" />
        </div>
        <div class="grid gap-2">
          <Label for="au-bio">Bio</Label>
          <Textarea id="au-bio" v-model="form.bio" rows="4" />
          <FormError :message="errors.bio" />
        </div>
        <div class="grid gap-2">
          <Label for="au-web">Website</Label>
          <Input id="au-web" v-model="form.website" type="url" placeholder="https://" :aria-invalid="!!errors.website" />
          <FormError :message="errors.website" />
        </div>
        <div class="grid gap-2">
          <Label>Profiles</Label>
          <div v-for="(s, i) in social" :key="i" class="flex gap-2">
            <Input v-model="s.key" placeholder="x" class="w-24 font-mono" aria-label="Profile name" />
            <Input v-model="s.url" type="url" placeholder="https://" class="min-w-0 flex-1" aria-label="Profile URL" />
            <Button type="button" variant="ghost" size="icon" aria-label="Remove profile" @click="social.splice(i, 1)">
              <X />
            </Button>
          </div>
          <Button type="button" variant="outline" size="sm" class="justify-self-start" @click="social.push({ key: '', url: '' })">
            <Plus /> Add profile
          </Button>
          <FormError :message="errors.social" />
        </div>
        <div v-if="can('users.manage')" class="grid gap-2">
          <Label>User account</Label>
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-sm" :class="form.user_id ? '' : 'text-muted-foreground'">{{ form.user_id ? userName : 'Guest author' }}</span>
            <Popover v-model:open="userOpen">
              <PopoverTrigger as-child>
                <Button type="button" variant="outline" size="sm">
                  {{ form.user_id ? 'Change' : 'Link a user' }}
                </Button>
              </PopoverTrigger>
              <PopoverContent align="start" class="w-80">
                <UserPicker @pick="onUser" />
              </PopoverContent>
            </Popover>
            <Button v-if="form.user_id" type="button" variant="ghost" size="sm" @click="form.user_id = null; userName = null">
              <Link2Off /> Unlink
            </Button>
          </div>
          <FormError :message="errors.user_id" />
        </div>
        <FormError :message="errors.form" />
      </form>
      <SheetFooter>
        <Button type="submit" form="author-form" :disabled="saving || !form.display_name.trim()">
          {{ saving ? 'Saving' : 'Save' }}
        </Button>
      </SheetFooter>
      <MediaPicker v-model:open="pickerOpen" accept="image" @select="onAvatar" />
    </SheetContent>
  </Sheet>
</template>
