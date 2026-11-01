<script setup lang="ts">
import PostList from '@/components/posts/PostList.vue'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'pages.edit' })
useHead({ title: 'Pages · AFSC' })
</script>

<template>
  <PostList type="page" />
</template>
