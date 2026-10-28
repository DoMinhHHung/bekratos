import { FormEvent, useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ApiError, authApi } from '../api/client'
import { GlassySimpleCard } from '../components/GlassyAuthShell'

export function VerifyEmailPage() {
  const [params] = useSearchParams()
  const [token, setToken] = useState(params.get('token') || '')
  const [identifier, setIdentifier] = useState('')
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [pending, setPending] = useState(false)
  const [resendPending, setResendPending] = useState(false)

  async function verify(value: string) {
    setError('')
    setSuccess('')
    setPending(true)
    try {
      const res = await authApi.verifyEmail(value.trim())
      setSuccess(res.message)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Verification failed')
    } finally {
      setPending(false)
    }
  }

  useEffect(() => {
    const t = params.get('token')
    if (t) void verify(t)
  }, [params])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    await verify(token)
  }

  async function onResend(e: FormEvent) {
    e.preventDefault()
    setError('')
    setSuccess('')
    setResendPending(true)
    try {
      const res = await authApi.resendVerification(identifier.trim())
      setSuccess(res.message)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Resend failed')
    } finally {
      setResendPending(false)
    }
  }

  return (
    <GlassySimpleCard title="Verify email" lead="Confirm your address to activate the account.">
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
          <label htmlFor="token">Verification token</label>
          <div className="g-input-row">
            <input id="token" required value={token} onChange={(e) => setToken(e.target.value)} />
          </div>
        </div>
        <button className="g-btn-primary" type="submit" disabled={pending}>
          {pending ? 'Verifying…' : 'Verify →'}
        </button>
      </form>

      <p className="g-lead" style={{ marginTop: 24 }}>
        Didn&apos;t get the email? Resend verification.
      </p>
      <form onSubmit={onResend}>
        <div className="g-field">
          <label htmlFor="identifier">Email or username</label>
          <div className="g-input-row">
            <input
              id="identifier"
              required
              value={identifier}
              onChange={(e) => setIdentifier(e.target.value)}
            />
          </div>
        </div>
        <button className="g-btn-primary" type="submit" disabled={resendPending}>
          {resendPending ? 'Sending…' : 'Resend verification →'}
        </button>
      </form>
    </GlassySimpleCard>
  )
}
