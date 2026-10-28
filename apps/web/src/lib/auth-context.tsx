import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { ApiError, authApi, type UserResult } from '../api/client'
import { clearTokens, isLoggedIn } from './auth-storage'

type AuthState = {
  user: UserResult | null
  loading: boolean
  setUser: (u: UserResult | null) => void
  refreshUser: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<UserResult | null>(null)
  const [loading, setLoading] = useState(true)

  const refreshUser = useCallback(async () => {
    if (!isLoggedIn()) {
      setUser(null)
      return
    }
    const me = await authApi.me()
    setUser(me)
  }, [])

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        if (isLoggedIn()) {
          const me = await authApi.me()
          if (!cancelled) setUser(me)
        }
      } catch (err) {
        if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
          clearTokens()
        }
        if (!cancelled) setUser(null)
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  const value = useMemo(
    () => ({ user, loading, setUser, refreshUser }),
    [user, loading, refreshUser],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth outside provider')
  return ctx
}
