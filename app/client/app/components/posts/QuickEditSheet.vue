<script setup lang="ts">
import type { TagChip } from '@/components/editor/TagPicker.vue'
import type { PostListItem, PostStatus } from '~/types/api'
import { reactive, ref, watch } from 'vue'
import CategoryPicker from '@/components/editor/CategoryPicker.vue'
import DateTimePicker from '@/components/editor/DateTimePicker.vue'
import TagPicker from '@/components/editor/TagPicker.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { API } from '~/composables/useAuth'

const props = defineProps<{ item: PostListItem | null }>()
const emit = defineEmits<{ saved: [] }>()
const open = defineModel<boolean>('open', { default: false })
const { authFetch, can } = useAuth()

const form = reactive({ title: '', slug: '', status: 'draft' as PostStatus, published_at: null as string | null, category_ids: [] as number[], tags: [] as TagChip[] })
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)

watch(() => props.item, (p) => {
  if (!p)
    return
  Object.assign(form, {
    title: p.title,
    slug: p.slug,
    status: p.status,
    published_at: p.published_at,
    category_ids: p.categories.map(c => c.id),
    tags: p.tags.map(t => ({ id: t.id, name: t.name })),
  })
  fieldErrors.value = {}
}, { immediate: true })

const statuses = (): PostStatus[] => {
  const canPublish = props.item?.type === 'page' ? can('pages.edit') : can('posts.publish_own')
  const list: PostStatus[] = canPublish ? ['draft', 'pending', 'published', 'private'] : ['draft', 'pending']
  return list.includes(form.status) ? list : [...list, form.status]
}

async function save() {
  const p = props.item
  if (!p)
    return
  saving.value = true
  fieldErrors.value = {}
  try {
    await authFetch(`${API}/posts/${p.id}`, {
      method: 'PATCH',
      body: {
        version: p.version,
        title: form.title,
        slug: form.slug,
        status: form.status,
        published_at: form.published_at,
        ...(p.type === 'post' ? { category_ids: form.category_ids, tag_ids: form.tags.map(t => t.id) } : {}),
      },
    })
    open.value = false
    emit('saved')
  }
  catch (e) {
    const { message, field, code } = errorData(e)
    fieldErrors.value = { [field ?? 'form']: code === 'conflict' ? 'Someone saved this in the meantime. Close and reopen to edit the latest version.' : (message ?? 'Could not save.') }
  }
  finally {
    saving.value = false
  }
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full overflow-y-auto sm:max-w-md">
      <SheetHeader>
        <SheetTitle>Quick edit</SheetTitle>
        <SheetDescription>Change the basics without opening the editor.</SheetDescription>
      </SheetHeader>
      <form id="quick-edit" class="grid gap-4 px-4" @submit.prevent="save">
        <div class="grid gap-2">
          <Label for="qe-title">Title</Label>
          <Input id="qe-title" v-model="form.title" :aria-invalid="!!fieldErrors.title" />
          <FormError :message="fieldErrors.title" />
        </div>
        <div class="grid gap-2">
          <Label for="qe-slug">Slug</Label>
          <Input id="qe-slug" v-model="form.slug" autocapitalize="none" spellcheck="false" :aria-invalid="!!fieldErrors.slug" />
          <FormError :message="fieldErrors.slug" />
        </div>
        <div class="grid gap-2">
          <Label for="qe-status">Status</Label>
          <NativeSelect id="qe-status" v-model="form.status" class="w-full">
            <NativeSelectOption v-for="s in statuses()" :key="s" :value="s">
              {{ s }}
            </NativeSelectOption>
          </NativeSelect>
          <FormError :message="fieldErrors.status" />
        </div>
        <div class="grid gap-2">
          <Label>Publish date</Label>
          <DateTimePicker v-model="form.published_at" />
          <FormError :message="fieldErrors.published_at" />
        </div>
        <template v-if="item?.type === 'post'">
          <div class="grid gap-2">
            <Label>Categories</Label>
            <CategoryPicker v-model:ids="form.category_ids" no-primary />
          </div>
          <div class="grid gap-2">
            <Label>Tags</Label>
            <TagPicker v-model="form.tags" />
          </div>
        </template>
        <FormError :message="fieldErrors.form" />
      </form>
      <SheetFooter>
        <Button type="submit" form="quick-edit" :disabled="saving">
          {{ saving ? 'Saving…' : 'Save' }}
        </Button>
      </SheetFooter>
    </SheetContent>
  </Sheet>
</template>
