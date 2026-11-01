<script setup lang="ts">
import PostList from '@/components/posts/PostList.vue'

definePageMeta({ middleware: ['auth', 'cap'], cap: 'posts.edit_own' })
useHead({ title: 'Posts · AFSC' })
</script>

<template>
  <PostList type="post" />
</template>
