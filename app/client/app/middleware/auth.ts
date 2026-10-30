export default defineNuxtRouteMiddleware(() => {
  if (!useAuth().user.value)
    return navigateTo('/signin')
})
