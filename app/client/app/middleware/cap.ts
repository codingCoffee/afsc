import type { Cap } from '@/composables/useAuth'

// pages declare `meta: { cap: '...' }`; without it every signed-in user may enter
export default defineNuxtRouteMiddleware((to) => {
  const cap = to.meta.cap as Cap | undefined
  if (cap && !useAuth().can(cap))
    return abortNavigation(createError({ statusCode: 403, statusMessage: 'Your role can\'t open this page' }))
})
