import { FormEvent, useState } from 'react'
import { ApiError, authApi } from '../api/client'
import { useAuth } from '../lib/auth-context'

export function AccountPage() {
  const { user, refreshUser } = useAuth()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [pending, setPending] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setSuccess('')
    setPending(true)
    try {
      const res = await authApi.changePassword(currentPassword, newPassword)
      setSuccess(res.message)
      setCurrentPassword('')
      setNewPassword('')
      await refreshUser()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">Account</h1>
        <p className="page-desc">Your profile information and password settings.</p>
      </div>
      <div className="grid-2">
        <div className="card">
          <h2>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="8" r="4" />
              <path d="M4 21c0-4 4-6 8-6s8 2 8 6" />
            </svg>
            Profile
          </h2>
          <div className="meta-row">
            <span className="meta-label">Full name</span>
            <span>{user?.fullName}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Email</span>
            <span className="meta-value">{user?.email}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Username</span>
            <span>{user?.username || '—'}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Status</span>
            <span className="badge badge-muted">{user?.status}</span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Verified at</span>
            <span className="meta-value">
              {user?.emailVerifiedAt
                ? new Date(user.emailVerifiedAt).toLocaleString()
                : '—'}
            </span>
          </div>
          <div className="meta-row">
            <span className="meta-label">Created</span>
            <span className="meta-value">
              {user?.createdAt ? new Date(user.createdAt).toLocaleString() : '—'}
            </span>
          </div>
        </div>
        <div className="card">
          <h2>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="11" width="18" height="11" rx="2" />
              <path d="M7 11V7a5 5 0 0 1 10 0v4" />
            </svg>
            Change password
          </h2>
          <p className="card-desc">POST /api/v1/auth/change-password</p>
          {error ? <div className="alert alert-error">{error}</div> : null}
          {success ? <div className="alert alert-success">{success}</div> : null}
          <form onSubmit={onSubmit} className="stack">
            <div className="field">
              <label htmlFor="current">Current password</label>
              <input
                id="current"
                type="password"
                required
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="next">New password</label>
              <input
                id="next"
                type="password"
                required
                minLength={8}
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
              />
            </div>
            <button className="btn btn-primary" type="submit" disabled={pending}>
              {pending ? 'Saving…' : 'Update password'}
            </button>
          </form>
        </div>
      </div>
    </div>
  )
}
