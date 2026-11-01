export default defineNuxtRouteMiddleware(async () => {
  if (useAuth().user.value)
    return
  if (await setupNeeded())
    return navigateTo('/setup')
  return navigateTo('/signin')
})
