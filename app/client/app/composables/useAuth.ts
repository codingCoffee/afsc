export type Role = 'admin' | 'editor' | 'author' | 'contributor'

export interface User {
  id: string
  username: string
  email: string
  first_name: string
  last_name: string
  display_name: string
  role: Role
  created_at: string
}

export interface Page<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}

interface Session {
  access_token: string
  expires_in: number
  user: User
}

export interface SignupInput {
  username: string
  email: string
  password: string
  first_name: string
  last_name: string
}

type FetchOptions = {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: Record<string, unknown>
}

const API = '/admin/api/v1'
const REFRESH_LEAD_MS = 30_000
const RETRY_MS = 10_000

let refreshTimer: ReturnType<typeof setTimeout> | undefined
let inflight: Promise<boolean> | null = null

export function statusOf(e: unknown): number | undefined {
  return (e as { statusCode?: number })?.statusCode
}

export function errorData(e: unknown): { message?: string, field?: string } {
  return (e as { data?: { message?: string, field?: string } })?.data ?? {}
}

export function useAuth() {
  const accessToken = useState<string | null>('auth:access', () => null)
  const user = useState<User | null>('auth:user', () => null)
  const expiresAt = useState<number>('auth:expires', () => 0)
  const router = useRouter()

  function schedule(delay: number) {
    clearTimeout(refreshTimer)
    refreshTimer = setTimeout(refresh, Math.max(delay, 5_000))
  }

  function apply(session: Session) {
    accessToken.value = session.access_token
    user.value = session.user
    expiresAt.value = Date.now() + session.expires_in * 1000
    schedule(expiresAt.value - Date.now() - REFRESH_LEAD_MS)
  }

  function clear() {
    clearTimeout(refreshTimer)
    accessToken.value = null
    user.value = null
    expiresAt.value = 0
  }

  async function doRefresh(): Promise<boolean> {
    try {
      apply(await $fetch<Session>(`${API}/token_refresh`, { method: 'POST' }))
      return true
    }
    catch (e) {
      if (statusOf(e) === 401) {
        clear()
        if ([router.currentRoute.value.meta.middleware].flat().includes('auth'))
          await router.push('/signin')
        return false
      }
      // network blip: keep the session and try again shortly
      if (user.value)
        schedule(RETRY_MS)
      return !!user.value
    }
  }

  // the lock serialises refreshes across tabs, so two tabs never present the same rotated token
  function refresh(): Promise<boolean> {
    if (inflight)
      return inflight
    const run = async (): Promise<boolean> => navigator.locks
      ? await navigator.locks.request('afsc-token-refresh', doRefresh)
      : doRefresh()
    inflight = run().finally(() => {
      inflight = null
    })
    return inflight
  }

  function refreshIfStale() {
    if (user.value && expiresAt.value - Date.now() < REFRESH_LEAD_MS)
      refresh()
  }

  async function signin(login: string, password: string) {
    apply(await $fetch<Session>(`${API}/signin`, { method: 'POST', body: { login, password } }))
  }

  async function signup(input: SignupInput) {
    apply(await $fetch<Session>(`${API}/signup`, { method: 'POST', body: input }))
  }

  async function signout() {
    try {
      await $fetch(`${API}/signout`, { method: 'POST' })
    }
    finally {
      clear()
      await navigateTo('/signin')
    }
  }

  async function authFetch<T>(url: string, opts: FetchOptions = {}): Promise<T> {
    const call = () => $fetch<T>(url, {
      ...opts,
      headers: accessToken.value ? { Authorization: `Bearer ${accessToken.value}` } : {},
    })
    try {
      return await call()
    }
    catch (e) {
      if (statusOf(e) !== 401 || !(await refresh()))
        throw e
      return await call()
    }
  }

  async function fetchMe() {
    user.value = await authFetch<User>(`${API}/me`)
    return user.value
  }

  return { user, accessToken, refresh, refreshIfStale, signin, signup, signout, authFetch, fetchMe }
}
