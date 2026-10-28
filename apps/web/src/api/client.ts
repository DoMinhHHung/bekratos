import { clearTokens, getAccessToken, getRefreshToken, setTokens } from '../lib/auth-storage'

const AUTH_BASE = (import.meta.env.VITE_AUTH_URL || '').replace(/\/$/, '')

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

  let res = await fetch(`${AUTH_BASE}${path}`, { ...rest, headers })

  if (res.status === 401 && auth && getRefreshToken()) {
    const refreshed = await ensureFreshAccessToken()
    if (refreshed) {
      headers.set('Authorization', `Bearer ${getAccessToken()}`)
      res = await fetch(`${AUTH_BASE}${path}`, { ...rest, headers })
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
    const res = await fetch(`${AUTH_BASE}/api/v1/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
    if (!res.ok) {
      clearTokens()
      return false
    }
    const data = (await res.json()) as {
      accessToken: string
      refreshToken: string
    }
    setTokens(data.accessToken, data.refreshToken)
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
  fullName: string
}

export type RegisterResult = {
  id: string
  email: string
  fullName: string
  status: string
}

export type TokenResult = {
  accessToken: string
  refreshToken: string
  tokenType: string
  expiresIn: number
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
    authFetch<RegisterResult>('/api/v1/auth/register', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  login: (identifier: string, password: string) =>
    authFetch<TokenResult>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ login: identifier, password }),
    }),

  logout: async () => {
    const refreshToken = getRefreshToken()
    try {
      if (refreshToken) {
        await authFetch('/api/v1/auth/logout', {
          method: 'POST',
          auth: true,
          body: JSON.stringify({ refresh_token: refreshToken }),
        })
      }
    } finally {
      clearTokens()
    }
  },

  me: () => authFetch<UserResult>('/v1/me', { auth: true }),

  verifyEmail: (token: string) =>
    authFetch<{ message: string }>('/api/v1/auth/verify-email', {
      method: 'POST',
      body: JSON.stringify({ token }),
    }),

  resendVerification: (identifier: string) =>
    authFetch<{ message: string }>('/api/v1/auth/resend-verification', {
      method: 'POST',
      body: JSON.stringify({ identifier }),
    }),

  forgotPassword: (identifier: string) =>
    authFetch<{ message: string }>('/api/v1/auth/forgot-password', {
      method: 'POST',
      body: JSON.stringify({ identifier }),
    }),

  resetPassword: (token: string, newPassword: string) =>
    authFetch<{ message: string }>('/api/v1/auth/reset-password', {
      method: 'POST',
      body: JSON.stringify({ token, newPassword }),
    }),

  changePassword: (currentPassword: string, newPassword: string) =>
    authFetch<{ message: string }>('/api/v1/auth/change-password', {
      method: 'POST',
      auth: true,
      body: JSON.stringify({ currentPassword, newPassword }),
    }),

  lookup: (q: string) =>
    authFetch<UserLookupResult>(`/api/v1/users/lookup?q=${encodeURIComponent(q)}`, {
      auth: true,
    }),

  health: () => authFetch<{ status: string }>('/actuator/health'),
}
