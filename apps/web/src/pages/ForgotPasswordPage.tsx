import { FormEvent, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError, authApi } from '../api/client'
import { GlassySimpleCard, IconMail } from '../components/GlassyAuthShell'

export function ForgotPasswordPage() {
  const [identifier, setIdentifier] = useState('')
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [pending, setPending] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setSuccess('')
    setPending(true)
    try {
      const res = await authApi.forgotPassword(identifier.trim())
      setSuccess(res.message)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <GlassySimpleCard
      title="Forgot password"
      lead="We will email a reset link if the account exists."
    >
      {error ? <div className="g-alert g-alert-error">{error}</div> : null}
      {success ? <div className="g-alert g-alert-success">{success}</div> : null}
      <form onSubmit={onSubmit}>
        <div className="g-field">
          <label htmlFor="identifier">Email or username</label>
          <div className="g-input-row">
            <IconMail />
            <input
              id="identifier"
              type="text"
              required
              value={identifier}
              onChange={(e) => setIdentifier(e.target.value)}
              placeholder="name@domain.com or username"
            />
          </div>
        </div>
        <button className="g-btn-primary" type="submit" disabled={pending}>
          {pending ? 'Sending…' : 'Send reset link →'}
        </button>
      </form>
      <p className="g-footer">
        <Link className="g-link" to="/login">
          Back to sign in
        </Link>
      </p>
    </GlassySimpleCard>
  )
}
