import { FormEvent, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ApiError, authApi } from '../api/client'
import { GlassySimpleCard, IconLock } from '../components/GlassyAuthShell'

export function ResetPasswordPage() {
  const [params] = useSearchParams()
  const [token, setToken] = useState(params.get('token') || '')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [pending, setPending] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setSuccess('')
    setPending(true)
    try {
      const res = await authApi.resetPassword(token.trim(), password)
      setSuccess(res.message)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Reset failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <GlassySimpleCard
      title="Reset password"
      lead="Paste the token from your email if it is not in the URL."
    >
      {error ? <div className="g-alert g-alert-error">{error}</div> : null}
      {success ? (
        <div className="g-alert g-alert-success">
          {success}{' '}
          <Link className="g-link" to="/login">
            Sign in
          </Link>
        </div>
      ) : null}
      <form onSubmit={onSubmit}>
        <div className="g-field">
          <label htmlFor="token">Token</label>
          <div className="g-input-row">
            <input id="token" required value={token} onChange={(e) => setToken(e.target.value)} />
          </div>
        </div>
        <div className="g-field">
          <label htmlFor="password">New password</label>
          <div className="g-input-row">
            <IconLock />
            <input
              id="password"
              type="password"
              required
              minLength={8}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
        </div>
        <button className="g-btn-primary" type="submit" disabled={pending}>
          {pending ? 'Saving…' : 'Update password →'}
        </button>
      </form>
    </GlassySimpleCard>
  )
}
