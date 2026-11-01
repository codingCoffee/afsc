<script setup lang="ts">
import { ShieldAlert } from '@lucide/vue'
import { onMounted } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { useCspReportOnly } from './groups'

const { can } = useAuth()
const { reportOnly, refresh } = useCspReportOnly()

onMounted(() => {
  if (reportOnly.value === null)
    refresh()
})
</script>

<template>
  <Alert v-if="reportOnly" class="mb-6 border-amber-500/50 text-amber-700 dark:text-amber-400">
    <ShieldAlert />
    <AlertTitle>CSP is in report-only mode</AlertTitle>
    <AlertDescription>
      <p>
        The public site's Content Security Policy logs violations but blocks nothing.
        <NuxtLink v-if="can('settings.manage')" to="/settings/security" class="underline underline-offset-2">
          Turn it off in Security settings
        </NuxtLink>
      </p>
    </AlertDescription>
  </Alert>
</template>
