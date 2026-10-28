import { clearTokens, getAccessToken, getRefreshToken, setTokens } from '../lib/auth-storage'

const AUTH_BASE = (import.meta.env.VITE_AUTH_URL || '').replace(/\/$/, '')

function authBaseUrl(): string {
  if (!AUTH_BASE) throw new Error('VITE_AUTH_URL is required. Set it to the Bekratos Identity API URL.')
  try {
    const url = new URL(AUTH_BASE)
    if (!['http:', 'https:'].includes(url.protocol) || /pizza/i.test(url.hostname)) throw new Error()
  } catch {
    throw new Error('VITE_AUTH_URL must be a valid HTTP(S) Bekratos Identity API URL.')
  }
  return AUTH_BASE
}

let refreshInFlight: Promise<boolean> | null = null

export class ApiError extends Error {
  status: number
  body: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.status = status
    this.body = body
  }
}

async function parseBody(res: Response): Promise<unknown> {
  const text = await res.text()
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

function messageFromBody(body: unknown, fallback: string): string {
  if (body && typeof body === 'object') {
    const o = body as Record<string, unknown>
    if (typeof o.message === 'string') return o.message
    if (typeof o.error === 'string') return o.error
    if (Array.isArray(o.errors) && o.errors.length) {
      return o.errors.map(String).join(', ')
    }
  }
  if (typeof body === 'string' && body.trim()) return body
  return fallback
}

export async function authFetch<T>(
  path: string,
  options: RequestInit & { auth?: boolean } = {},
): Promise<T> {
  const { auth = false, headers: initHeaders, ...rest } = options
  const headers = new Headers(initHeaders)
  if (!headers.has('Content-Type') && rest.body) {
    headers.set('Content-Type', 'application/json')
  }
  if (auth) {
    const token = getAccessToken()
    if (token) headers.set('Authorization', `Bearer ${token}`)
  }

  let res = await fetch(`${authBaseUrl()}${path}`, { ...rest, headers })

  if (res.status === 401 && auth && getRefreshToken()) {
    const refreshed = await ensureFreshAccessToken()
    if (refreshed) {
      headers.set('Authorization', `Bearer ${getAccessToken()}`)
      res = await fetch(`${authBaseUrl()}${path}`, { ...rest, headers })
    }
  }

  const body = await parseBody(res)
  if (!res.ok) {
    throw new ApiError(res.status, messageFromBody(body, res.statusText), body)
  }
  return body as T
}

/** Exported so control.ts can reuse the same refresh path. */
export async function ensureFreshAccessToken(): Promise<boolean> {
  if (!getRefreshToken()) return false
  if (!refreshInFlight) {
    refreshInFlight = tryRefresh().finally(() => {
      refreshInFlight = null
    })
  }
  return refreshInFlight
}

async function tryRefresh(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false
  try {
    const res = await fetch(`${authBaseUrl()}/v1/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
    if (!res.ok) {
      clearTokens()
      return false
    }
    const data = (await res.json()) as {
      access_token: string
      refresh_token: string
    }
    if (!data.access_token || !data.refresh_token) {
      clearTokens()
      return false
    }
    setTokens(data.access_token, data.refresh_token)
    return true
  } catch {
    clearTokens()
    return false
  }
}

export type RegisterPayload = {
  email: string
  username: string
  password: string
  accept_policy: boolean
  locale?: string
  timezone?: string
  full_name?: string
}

export type RegisterResult = { message: string }

export type TokenResult = {
  access_token: string
  refresh_token: string
  token_type: string
  expires_in: number
}

export type UserResult = {
  id: string
  email: string
  username?: string
  fullName: string
  status: string
  emailVerifiedAt: string | null
  createdAt: string
}

export type UserLookupResult = {
  id: string
  email: string
  username?: string
  fullName: string
}

export const authApi = {
  register: (data: RegisterPayload) =>
    authFetch<RegisterResult>('/v1/auth/register', {
      method: 'POST',
      body: JSON.stringify({ ...data, accept_policy: data.accept_policy ?? true, full_name: data.full_name || undefined }),
    }),

  login: (identifier: string, password: string) =>
    authFetch<TokenResult>('/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ login: identifier, password }),
    }),

  logout: async () => {
    const refreshToken = getRefreshToken()
    try {
      if (refreshToken) {
        await authFetch('/v1/auth/logout', {
          method: 'POST',
          auth: true,
        })
      }
    } finally {
      clearTokens()
    }
  },

  me: () => authFetch<UserResult>('/v1/me', { auth: true }),

  verifyEmail: (token: string) =>
    authFetch<{ message: string }>('/v1/auth/verify-email', {
      method: 'POST',
      body: JSON.stringify({ token }),
    }),

  resendVerification: (identifier: string) =>
    authFetch<{ message: string }>('/v1/auth/resend-verification', {
      method: 'POST',
      body: JSON.stringify({ email: identifier }),
    }),

  forgotPassword: (identifier: string) =>
    authFetch<{ message: string }>('/v1/auth/forgot-password', {
      method: 'POST',
      body: JSON.stringify({ email: identifier }),
    }),

  resetPassword: (token: string, newPassword: string) =>
    authFetch<{ message: string }>('/v1/auth/reset-password', {
      method: 'POST',
      body: JSON.stringify({ token, new_password: newPassword }),
    }),

  changePassword: (currentPassword: string, newPassword: string) =>
    authFetch<{ message: string }>('/v1/auth/change-password', {
      method: 'POST',
      auth: true,
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    }),

  lookup: (q: string) =>
    authFetch<UserLookupResult>(`/v1/users/lookup?q=${encodeURIComponent(q)}`, {
      auth: true,
    }),

  health: () => authFetch<{ status: string }>('/actuator/health'),
}
