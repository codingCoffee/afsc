<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import type { Author, MenuItemInput, PostListItem, Term } from '@/types/api'
import { reactive, ref, watch } from 'vue'
import AuthorPicker from '@/components/taxonomy/AuthorPicker.vue'
import SearchPicker from '@/components/taxonomy/SearchPicker.vue'
import TermPicker from '@/components/taxonomy/TermPicker.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

// title is the target's name, shown in the tree until the menu is saved and reloaded
const emit = defineEmits<{ add: [item: MenuItemInput, title: string] }>()
const open = defineModel<boolean>('open', { default: false })

const { authFetch } = useAuth()
const tab = ref('post')
const postType = ref<'post' | 'page'>('page')
const link = reactive({ label: '', url: '' })

watch(open, (v) => {
  if (v)
    Object.assign(link, { label: '', url: '' })
})

async function searchPosts(q: string) {
  return (await authFetch<Page<PostListItem>>('/admin/api/v1/posts', { query: { q, type: postType.value, page_size: 20 } })).items
}

function add(kind: MenuItemInput['kind'], label: string, target: number | null, url: string | null) {
  emit('add', { label, kind, target_id: target, url, new_tab: false, css_classes: [], children: [] }, label)
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>Add menu item</DialogTitle>
        <DialogDescription>Link to a page or post, an archive, or any URL.</DialogDescription>
      </DialogHeader>
      <Tabs v-model="tab">
        <TabsList class="w-full">
          <TabsTrigger value="post">
            Content
          </TabsTrigger>
          <TabsTrigger value="term">
            Archive
          </TabsTrigger>
          <TabsTrigger value="author">
            Author
          </TabsTrigger>
          <TabsTrigger value="url">
            Link
          </TabsTrigger>
        </TabsList>
        <TabsContent value="post" class="grid gap-2">
          <NativeSelect v-model="postType" aria-label="Type">
            <NativeSelectOption value="page">
              Pages
            </NativeSelectOption>
            <NativeSelectOption value="post">
              Posts
            </NativeSelectOption>
          </NativeSelect>
          <SearchPicker
            :key="postType"
            :search="searchPosts"
            :label="(p: PostListItem) => p.title || '(untitled)'"
            :hint="(p: PostListItem) => p.status"
            :item-key="(p: PostListItem) => p.id"
            placeholder="Search by title"
            @pick="(p: PostListItem) => add('post', p.title, p.id, null)"
          />
        </TabsContent>
        <TabsContent value="term">
          <TermPicker placeholder="Search categories and tags" @pick="(t: Term) => add('term', t.name, t.id, null)" />
        </TabsContent>
        <TabsContent value="author">
          <AuthorPicker @pick="(a: Author) => add('author', a.display_name, a.id, null)" />
        </TabsContent>
        <TabsContent value="url">
          <form class="grid gap-3" @submit.prevent="add('url', link.label.trim(), null, link.url.trim())">
            <div class="grid gap-2">
              <Label for="mi-label">Label</Label>
              <Input id="mi-label" v-model="link.label" required />
            </div>
            <div class="grid gap-2">
              <Label for="mi-url">URL</Label>
              <Input id="mi-url" v-model="link.url" required placeholder="https://… or /path" class="font-mono" />
            </div>
            <Button type="submit" class="justify-self-start" :disabled="!link.label.trim() || !link.url.trim()">
              Add link
            </Button>
          </form>
        </TabsContent>
      </Tabs>
    </DialogContent>
  </Dialog>
</template>
