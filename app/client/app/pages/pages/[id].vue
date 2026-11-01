<script setup lang="ts">
import PostEditor from '@/components/posts/PostEditor.vue'

// a stable key keeps the editor mounted when a new page's URL changes to its id after the first save
definePageMeta({ middleware: ['auth', 'cap'], cap: 'pages.edit', key: 'page-editor' })
</script>

<template>
  <PostEditor type="page" />
</template>
