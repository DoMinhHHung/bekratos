const ACCESS = 'bekratos_access_token'
const REFRESH = 'bekratos_refresh_token'

export function getAccessToken(): string | null {
  return localStorage.getItem(ACCESS)
}

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH)
}

export function setTokens(accessToken: string, refreshToken: string) {
  localStorage.setItem(ACCESS, accessToken)
  localStorage.setItem(REFRESH, refreshToken)
}

export function clearTokens() {
  localStorage.removeItem(ACCESS)
  localStorage.removeItem(REFRESH)
}

export function isLoggedIn(): boolean {
  return Boolean(getAccessToken())
}
