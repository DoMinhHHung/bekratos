import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { authApi } from '../api/client'
import { useAuth } from '../lib/auth-context'

export function OverviewPage() {
  const { user } = useAuth()
  const [health, setHealth] = useState<string>('…')

  useEffect(() => {
    authApi
      .health()
      .then((h) => setHealth(h.status))
      .catch(() => setHealth('DOWN'))
  }, [])

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">Overview</h1>
        <p className="page-desc">
          Your session is live. Manage{' '}
          <Link to="/app/orgs" style={{ color: 'var(--accent-hover)' }}>
            organizations
          </Link>{' '}
          via the control-plane API.
        </p>
      </div>
      <div className="grid-2">
        <div className="card card-accent">
          <h2>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="8" r="4" />
              <path d="M4 21c0-4 4-6 8-6s8 2 8 6" />
            </svg>
            Session
          </h2>
          <p className="card-desc">GET /api/v1/auth/me</p>
          <div className="meta-row">
            <span className="meta-label">Name</span>
            <span>{user?.fullName}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Email</span>
            <span className="meta-value">{user?.email}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Status</span>
            <span className={user?.status === 'ACTIVE' ? 'badge badge-ok' : 'badge badge-warn'}>
              {user?.status}
            </span>
          </div>
          <div className="meta-row">
            <span className="meta-label">User ID</span>
            <span className="meta-value">{user?.id}</span>
          </div>
        </div>
        <div className="card">
          <h2>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M22 12h-4l-3 9L9 3l-3 9H2" />
            </svg>
            Services
          </h2>
          <p className="card-desc">Backend connectivity</p>
          <div className="meta-row">
            <span className="meta-label">Auth health</span>
            <span className={health === 'UP' ? 'badge badge-ok' : 'badge badge-warn'}>{health}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Auth URL</span>
            <span className="meta-value">{import.meta.env.VITE_AUTH_URL}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Control URL</span>
            <span className="meta-value">{import.meta.env.VITE_CONTROL_URL || 'http://localhost:8081'}</span>
          </div>
        </div>
      </div>
    </div>
  )
}
