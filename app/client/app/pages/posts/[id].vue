<script setup lang="ts">
import PostEditor from '@/components/posts/PostEditor.vue'

// a stable key keeps the editor mounted when a new post's URL changes to its id after the first save
definePageMeta({ middleware: ['auth', 'cap'], cap: 'posts.edit_own', key: 'post-editor' })
</script>

<template>
  <PostEditor type="post" />
</template>
