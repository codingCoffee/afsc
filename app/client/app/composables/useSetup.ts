import { API } from '~/composables/useAuth'

// setupNeeded asks the API once whether the site still has no users; the answer only ever goes from true to false.
export async function setupNeeded(): Promise<boolean> {
  const state = useState<boolean | null>('setup:needed', () => null)
  if (state.value === false)
    return false
  try {
    await $fetch(`${API}/setup/status`)
    state.value = true
  }
  catch {
    state.value = false
  }
  return state.value
}

export function markSetupDone() {
  useState<boolean | null>('setup:needed').value = false
}
