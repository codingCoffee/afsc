export default defineNuxtRouteMiddleware(async (to) => {
  if (useAuth().user.value)
    return navigateTo('/dashboard')
  if (to.path !== '/setup' && await setupNeeded())
    return navigateTo('/setup')
})
