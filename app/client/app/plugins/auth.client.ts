export default defineNuxtPlugin(async () => {
  const { refresh, refreshIfStale } = useAuth()

  await refresh()

  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible')
      refreshIfStale()
  })
})
