<script setup lang="ts">
import type { Page } from '@/composables/useAuth'
import { ArrowLeft } from '@lucide/vue'
import { ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { API } from '~/composables/useAuth'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'content.manage' })
useHead({ title: 'Legacy URLs · AFSC' })

interface LegacyRow { path: string, media_id: number, hits: number, last_hit_at: string | null }

const PAGE_SIZE = 50
const { authFetch } = useAuth()
const page = ref(1)
const data = ref<Page<LegacyRow> | null>(null)
const loading = ref(false)
const error = ref('')
const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await authFetch<Page<LegacyRow>>(`${API}/media/legacy`, { query: { page: page.value, page_size: PAGE_SIZE } })
  }
  catch (e) {
    error.value = errorData(e).message ?? 'Could not load legacy URLs.'
  }
  finally {
    loading.value = false
  }
}
watch(page, load)
load()
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Legacy URLs" description="Imported WordPress upload paths, most requested first. They serve the file directly, so replacing or deleting the file changes what they return.">
      <template #actions>
        <Button variant="outline" size="sm" as-child>
          <NuxtLink to="/media">
            <ArrowLeft />
            Media
          </NuxtLink>
        </Button>
      </template>
    </PageHeader>

    <FormError :message="error" />

    <div class="rounded-md border" :class="{ 'opacity-60': loading }">
      <Table>
        <TableHeader>
          <TableRow class="font-mono text-[10px] tracking-widest uppercase">
            <TableHead>Path</TableHead>
            <TableHead class="text-right">
              Hits
            </TableHead>
            <TableHead class="hidden md:table-cell">
              Last hit
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in data?.items ?? []" :key="r.path">
            <TableCell class="max-w-0 w-full">
              <NuxtLink :to="{ path: '/media', query: { id: r.media_id } }" class="block truncate font-mono text-xs hover:underline" :title="r.path">
                {{ r.path }}
              </NuxtLink>
            </TableCell>
            <TableCell class="text-right font-mono text-xs">
              {{ r.hits }}
            </TableCell>
            <TableCell class="hidden font-mono text-xs text-muted-foreground md:table-cell">
              {{ r.last_hit_at ? fmt.format(new Date(r.last_hit_at)) : 'never' }}
            </TableCell>
          </TableRow>
          <TableEmpty v-if="data && !data.items.length" :colspan="3">
            No legacy upload paths. They come from a WordPress import.
          </TableEmpty>
        </TableBody>
      </Table>
    </div>
    <PagePagination v-if="data" v-model:page="page" :total="data.total" :page-size="PAGE_SIZE" />
  </div>
</template>
