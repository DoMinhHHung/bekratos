import { FormEvent, useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api/client'
import { controlApi, type Organization } from '../api/control'

export function OrgsPage() {
  const [orgs, setOrgs] = useState<Organization[]>([])
  const [error, setError] = useState('')
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [pending, setPending] = useState(false)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setError('')
    try {
      setOrgs(await controlApi.listOrgs())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load organizations')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onCreate(e: FormEvent) {
    e.preventDefault()
    setPending(true)
    setError('')
    try {
      await controlApi.createOrg(name.trim(), slug.trim())
      setName('')
      setSlug('')
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Create failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">Organizations</h1>
        <p className="page-desc">
          Create and open an organization to manage members, projects, and API keys.
        </p>
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}

      <div className="grid-2">
        <div className="card">
          <h2>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 21h18" />
              <path d="M5 21V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v16" />
              <path d="M13 21V9a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v12" />
            </svg>
            Your organizations
          </h2>
          {loading ? (
            <p className="card-desc">Loading…</p>
          ) : orgs.length === 0 ? (
            <div className="empty">No organizations yet. Create one to get started.</div>
          ) : (
            <div className="stack">
              {orgs.map((o) => (
                <a key={o.id} href={`/app/orgs/${o.id}`} className="org-card-link">
                  <div className="org-card-link-title">{o.name}</div>
                  <div className="org-card-link-meta">
                    {o.slug}
                    <span className="badge badge-muted">{o.role}</span>
                  </div>
                </a>
              ))}
            </div>
          )}
        </div>
        <div className="card">
          <h2>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            Create organization
          </h2>
          <p className="card-desc">POST /api/v1/orgs — you become OWNER</p>
          <form onSubmit={onCreate} className="stack">
            <div className="field">
              <label htmlFor="name">Name</label>
              <input id="name" required value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="field">
              <label htmlFor="slug">Slug</label>
              <input
                id="slug"
                required
                pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
                placeholder="acme-inc"
                value={slug}
                onChange={(e) => setSlug(e.target.value)}
              />
            </div>
            <button className="btn btn-primary" type="submit" disabled={pending}>
              {pending ? 'Creating…' : 'Create organization'}
            </button>
          </form>
        </div>
      </div>
    </div>
  )
}
