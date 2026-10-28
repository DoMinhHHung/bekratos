import { getAccessToken } from '../lib/auth-storage'
import { ApiError, ensureFreshAccessToken } from './client'

const CONTROL_BASE = (import.meta.env.VITE_CONTROL_URL || 'http://localhost:8081').replace(/\/$/, '')

async function controlFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type') && options.body) {
    headers.set('Content-Type', 'application/json')
  }

  const doFetch = async () => {
    const token = getAccessToken()
    if (token) headers.set('Authorization', `Bearer ${token}`)
    else headers.delete('Authorization')
    return fetch(`${CONTROL_BASE}${path}`, { ...options, headers })
  }

  let res = await doFetch()
  if (res.status === 401) {
    const ok = await ensureFreshAccessToken()
    if (ok) res = await doFetch()
  }

  if (res.status === 204) return undefined as T
  const text = await res.text()
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = text
    }
  }
  if (!res.ok) {
    const msg =
      body && typeof body === 'object' && 'message' in body
        ? String((body as { message: string }).message)
        : res.statusText
    throw new ApiError(res.status, msg, body)
  }
  return body as T
}

export type Organization = {
  id: string
  name: string
  slug: string
  role: string
  createdAt?: string
  updatedAt?: string
}

export type Member = {
  orgId: string
  userId: string
  role: string
  createdAt?: string
}

export type Project = {
  id: string
  orgId: string
  name: string
  slug: string
  createdAt?: string
  updatedAt?: string
}

export type ApiKey = {
  id: string
  projectId: string
  name: string
  keyPrefix: string
  createdBy: string
  createdAt?: string
  revokedAt?: string | null
  lastUsedAt?: string | null
}

export const controlApi = {
  listOrgs: () =>
    controlFetch<{ organizations: Organization[] }>('/api/v1/orgs').then(
      (r) => r.organizations || [],
    ),

  getOrg: (orgId: string) => controlFetch<Organization>(`/api/v1/orgs/${orgId}`),

  createOrg: (name: string, slug: string) =>
    controlFetch<Organization>('/api/v1/orgs', {
      method: 'POST',
      body: JSON.stringify({ name, slug }),
    }),

  updateOrg: (orgId: string, name: string, slug: string) =>
    controlFetch<Organization>(`/api/v1/orgs/${orgId}`, {
      method: 'PATCH',
      body: JSON.stringify({ name, slug }),
    }),

  deleteOrg: (orgId: string) =>
    controlFetch<void>(`/api/v1/orgs/${orgId}`, { method: 'DELETE' }),

  listMembers: (orgId: string) =>
    controlFetch<{ members: Member[] }>(`/api/v1/orgs/${orgId}/members`).then(
      (r) => r.members || [],
    ),

  addMember: (orgId: string, userId: string, role: string) =>
    controlFetch<Member>(`/api/v1/orgs/${orgId}/members`, {
      method: 'POST',
      body: JSON.stringify({ userId, role }),
    }),

  updateMemberRole: (orgId: string, userId: string, role: string) =>
    controlFetch<Member>(`/api/v1/orgs/${orgId}/members/${userId}`, {
      method: 'PATCH',
      body: JSON.stringify({ role }),
    }),

  removeMember: (orgId: string, userId: string) =>
    controlFetch<void>(`/api/v1/orgs/${orgId}/members/${userId}`, { method: 'DELETE' }),

  listProjects: (orgId: string) =>
    controlFetch<{ projects: Project[] }>(`/api/v1/orgs/${orgId}/projects`).then(
      (r) => r.projects || [],
    ),

  getProject: (projectId: string) => controlFetch<Project>(`/api/v1/projects/${projectId}`),

  createProject: (orgId: string, name: string, slug: string) =>
    controlFetch<Project>(`/api/v1/orgs/${orgId}/projects`, {
      method: 'POST',
      body: JSON.stringify({ name, slug }),
    }),

  updateProject: (projectId: string, name: string, slug: string) =>
    controlFetch<Project>(`/api/v1/projects/${projectId}`, {
      method: 'PATCH',
      body: JSON.stringify({ name, slug }),
    }),

  deleteProject: (projectId: string) =>
    controlFetch<void>(`/api/v1/projects/${projectId}`, { method: 'DELETE' }),

  listApiKeys: (projectId: string) =>
    controlFetch<{ apiKeys: ApiKey[] }>(`/api/v1/projects/${projectId}/api-keys`).then(
      (r) => r.apiKeys || [],
    ),

  createApiKey: (projectId: string, name: string) =>
    controlFetch<{ apiKey: ApiKey; secret: string }>(`/api/v1/projects/${projectId}/api-keys`, {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  revokeApiKey: (id: string) =>
    controlFetch<void>(`/api/v1/api-keys/${id}`, { method: 'DELETE' }),
}
