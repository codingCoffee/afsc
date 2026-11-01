<script setup lang="ts">
import type { Menu, MenuItem, MenuItemInput } from '@/types/api'
import { ArrowDown, ArrowUp, IndentDecrease, IndentIncrease, Plus, Trash2, X } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import MenuItemPicker from '@/components/taxonomy/MenuItemPicker.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: 'Menus · AFSC' })

interface Location { location: string, label: string, menu_id: number | null }
interface Node {
  key: number
  label: string
  kind: MenuItemInput['kind']
  target_id: number | null
  url: string | null
  new_tab: boolean
  classes: string
  title: string | null
  resolved: string | null
  children: Node[]
}

const API = '/admin/api/v1'
const MAX_DEPTH = 5
const { authFetch } = useAuth()
const menus = ref<Menu[]>([])
const locations = ref<Location[]>([])
const selected = ref('')
const name = ref('')
const slug = ref('')
const items = ref<Node[]>([])
const saved = ref('')
const error = ref('')
const saving = ref(false)
let seq = 0

function toNode(it: MenuItem): Node {
  return {
    key: ++seq,
    label: it.label,
    kind: it.kind,
    target_id: it.target_id,
    url: it.url,
    new_tab: it.new_tab,
    classes: it.css_classes.join(' '),
    title: it.target_title,
    resolved: it.resolved_url,
    children: it.children.map(toNode),
  }
}

function toInput(n: Node): MenuItemInput {
  return {
    label: n.label,
    kind: n.kind,
    target_id: n.kind === 'url' ? null : n.target_id,
    url: n.kind === 'url' ? (n.url ?? '') : null,
    new_tab: n.new_tab,
    css_classes: n.classes.split(/[\s,]+/).filter(Boolean),
    children: n.children.map(toInput),
  }
}

const body = computed(() => JSON.stringify({ name: name.value, slug: slug.value, items: items.value.map(toInput) }))
const dirty = computed(() => !!selected.value && body.value !== saved.value)

function edit(m: Menu | undefined) {
  name.value = m?.name ?? ''
  slug.value = m?.slug ?? ''
  items.value = (m?.items ?? []).map(toNode)
  saved.value = body.value
  error.value = ''
}

async function load(keep?: number) {
  try {
    const res = await authFetch<{ menus: Menu[], locations: Location[] }>(`${API}/menus`)
    menus.value = res.menus
    locations.value = res.locations
    const id = keep ?? Number(selected.value)
    const m = res.menus.find(x => x.id === id) ?? res.menus[0]
    selected.value = m ? String(m.id) : ''
    edit(m)
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load menus.'
  }
}
load()

watch(selected, (v, old) => {
  if (old && v !== old)
    edit(menus.value.find(m => String(m.id) === v))
})

// the tree flattened for display, with what each row's buttons need
interface Row { node: Node, depth: number, siblings: Node[], index: number, parent: Row | null }
const rows = computed(() => {
  const out: Row[] = []
  const walk = (list: Node[], depth: number, parent: Row | null) => {
    list.forEach((node, index) => {
      const row = { node, depth, siblings: list, index, parent }
      out.push(row)
      walk(node.children, depth + 1, row)
    })
  }
  walk(items.value, 1, null)
  return out
})

function height(n: Node): number {
  return 1 + Math.max(0, ...n.children.map(height))
}

function move(r: Row, by: -1 | 1) {
  const j = r.index + by
  if (j < 0 || j >= r.siblings.length)
    return
  const [n] = r.siblings.splice(r.index, 1)
  r.siblings.splice(j, 0, n!)
}

function canIndent(r: Row) {
  return r.index > 0 && r.depth + height(r.node) <= MAX_DEPTH
}

function indent(r: Row) {
  if (!canIndent(r))
    return
  const prev = r.siblings[r.index - 1]!
  r.siblings.splice(r.index, 1)
  prev.children.push(r.node)
}

function outdent(r: Row) {
  const p = r.parent
  if (!p)
    return
  r.siblings.splice(r.index, 1)
  p.siblings.splice(p.index + 1, 0, r.node)
}

function remove(r: Row) {
  r.siblings.splice(r.index, 1)
}

const pickerOpen = ref(false)
function onAdd(it: MenuItemInput, title: string) {
  items.value.push({ key: ++seq, label: it.label, kind: it.kind, target_id: it.target_id, url: it.url, new_tab: false, classes: '', title: it.kind === 'url' ? null : title, resolved: it.url, children: [] })
}

async function save() {
  if (!selected.value)
    return
  saving.value = true
  error.value = ''
  try {
    const m = await authFetch<Menu>(`${API}/menus/${selected.value}`, { method: 'PUT', body: JSON.parse(body.value) })
    menus.value = menus.value.map(x => (x.id === m.id ? m : x))
    edit(m)
    toast.success('Menu saved.')
  }
  catch (e) {
    const { message, field } = errorData(e)
    error.value = field ? `${field}: ${message}` : (message ?? 'Could not save the menu.')
  }
  finally {
    saving.value = false
  }
}

async function assign(loc: string, value: string) {
  try {
    const res = await authFetch<{ menus: Menu[], locations: Location[] }>(`${API}/menu-locations`, {
      method: 'PUT',
      body: { locations: { [loc]: value ? Number(value) : null } },
    })
    locations.value = res.locations
    toast.success('Location updated.')
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not update the location.')
  }
}

const createOpen = ref(false)
const newName = ref('')
const createError = ref('')
async function create() {
  createError.value = ''
  try {
    const m = await authFetch<Menu>(`${API}/menus`, { method: 'POST', body: { name: newName.value, slug: '' } })
    createOpen.value = false
    newName.value = ''
    await load(m.id)
  }
  catch (e) {
    createError.value = errorData(e).message ?? 'Could not create the menu.'
  }
}

const confirmOpen = ref(false)
async function removeMenu() {
  try {
    await authFetch(`${API}/menus/${selected.value}`, { method: 'DELETE' })
    toast.success('Menu deleted.')
    selected.value = ''
    await load()
  }
  catch (e) {
    toast.error(errorData(e).message ?? 'Could not delete the menu.')
  }
}

const kindLabel: Record<MenuItemInput['kind'], string> = { post: 'content', term: 'archive', author: 'author', url: 'link' }
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Menus" description="Build menus, then place them in the theme's menu locations. Saving purges every cached page.">
      <template #actions>
        <Button size="sm" variant="outline" @click="createOpen = true">
          <Plus />
          New menu
        </Button>
      </template>
    </PageHeader>

    <section v-if="locations.length" class="grid gap-3 rounded-md border p-4">
      <h2 class="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
        Locations
      </h2>
      <div class="grid gap-3 sm:grid-cols-2">
        <div v-for="l in locations" :key="l.location" class="grid gap-1.5">
          <Label :for="`loc-${l.location}`">{{ l.label }}</Label>
          <NativeSelect :id="`loc-${l.location}`" :model-value="l.menu_id ? String(l.menu_id) : ''" class="w-full" @update:model-value="v => assign(l.location, String(v ?? ''))">
            <NativeSelectOption value="">
              No menu
            </NativeSelectOption>
            <NativeSelectOption v-for="m in menus" :key="m.id" :value="String(m.id)">
              {{ m.name }}
            </NativeSelectOption>
          </NativeSelect>
        </div>
      </div>
    </section>

    <p v-if="!menus.length" class="rounded-md border py-12 text-center text-sm text-muted-foreground">
      No menus yet.
    </p>

    <section v-else class="grid gap-4">
      <div class="flex flex-wrap items-end gap-3">
        <div class="grid gap-1.5">
          <Label for="menu-select">Menu</Label>
          <NativeSelect id="menu-select" v-model="selected">
            <NativeSelectOption v-for="m in menus" :key="m.id" :value="String(m.id)">
              {{ m.name }}
            </NativeSelectOption>
          </NativeSelect>
        </div>
        <div class="grid gap-1.5">
          <Label for="menu-name">Name</Label>
          <Input id="menu-name" v-model="name" class="w-48" />
        </div>
        <div class="grid gap-1.5">
          <Label for="menu-slug">Slug</Label>
          <Input id="menu-slug" v-model="slug" class="w-40 font-mono" autocapitalize="none" spellcheck="false" />
        </div>
        <div class="flex gap-2 sm:ml-auto">
          <Button variant="outline" size="sm" class="text-destructive" @click="confirmOpen = true">
            <Trash2 />
            Delete menu
          </Button>
          <Button size="sm" :disabled="!dirty || saving" @click="save">
            {{ saving ? 'Saving' : 'Save menu' }}
          </Button>
        </div>
      </div>
      <FormError :message="error" />

      <ul class="grid gap-2">
        <li
          v-for="r in rows"
          :key="r.node.key"
          class="grid gap-2 rounded-md border p-2.5"
          :style="{ marginLeft: `${Math.min(r.depth - 1, 4) * 1.5}rem` }"
        >
          <div class="flex flex-wrap items-center gap-1.5">
            <div class="flex">
              <Button variant="ghost" size="icon-sm" aria-label="Move up" :disabled="r.index === 0" @click="move(r, -1)">
                <ArrowUp />
              </Button>
              <Button variant="ghost" size="icon-sm" aria-label="Move down" :disabled="r.index === r.siblings.length - 1" @click="move(r, 1)">
                <ArrowDown />
              </Button>
              <Button variant="ghost" size="icon-sm" aria-label="Outdent" :disabled="!r.parent" @click="outdent(r)">
                <IndentDecrease />
              </Button>
              <Button variant="ghost" size="icon-sm" aria-label="Indent" :disabled="!canIndent(r)" @click="indent(r)">
                <IndentIncrease />
              </Button>
            </div>
            <Input v-model="r.node.label" aria-label="Label" class="h-8 min-w-0 flex-1 basis-40" />
            <Badge variant="outline" class="rounded-sm text-[10px] uppercase">
              {{ kindLabel[r.node.kind] }}
            </Badge>
            <Button variant="ghost" size="icon-sm" aria-label="Remove item" @click="remove(r)">
              <X />
            </Button>
          </div>
          <div class="flex flex-wrap items-center gap-2 pl-1 text-xs text-muted-foreground">
            <Input v-if="r.node.kind === 'url'" v-model="r.node.url" aria-label="URL" placeholder="https://… or /path" class="h-7 min-w-0 flex-1 basis-48 font-mono text-xs" />
            <span v-else class="min-w-0 flex-1 truncate">
              → {{ r.node.title ?? 'missing target' }}
              <span v-if="r.node.title && !r.node.resolved" class="text-destructive">(not public, hidden)</span>
            </span>
            <Input v-model="r.node.classes" aria-label="CSS classes" placeholder="css classes" class="h-7 w-36 font-mono text-xs" />
            <label class="flex items-center gap-1.5">
              <Checkbox v-model="r.node.new_tab" />
              New tab
            </label>
          </div>
        </li>
      </ul>
      <Button variant="outline" size="sm" class="justify-self-start" @click="pickerOpen = true">
        <Plus />
        Add item
      </Button>
    </section>

    <MenuItemPicker v-model:open="pickerOpen" @add="onAdd" />

    <Dialog v-model:open="createOpen">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>New menu</DialogTitle>
          <DialogDescription>Place it in a location once it has items.</DialogDescription>
        </DialogHeader>
        <form id="menu-create" class="grid gap-2" @submit.prevent="create">
          <Label for="new-menu">Name</Label>
          <Input id="new-menu" v-model="newName" required />
          <FormError :message="createError" />
        </form>
        <DialogFooter>
          <Button type="submit" form="menu-create" :disabled="!newName.trim()">
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Delete ${name}?`"
      description="The menu and its items go away, and any location using it shows no menu."
      action="Delete menu"
      @confirm="removeMenu"
    />
  </div>
</template>
