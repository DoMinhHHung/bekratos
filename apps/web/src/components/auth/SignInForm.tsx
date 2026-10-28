import { FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { IconLock, IconMail } from '../GlassyAuthShell'

type Props = {
  identifier: string
  password: string
  showPassword: boolean
  error: string
  pending: boolean
  onIdentifierChange: (v: string) => void
  onPasswordChange: (v: string) => void
  onTogglePassword: () => void
  onSubmit: (e: FormEvent) => void
}

export function SignInForm({
  identifier,
  password,
  showPassword,
  error,
  pending,
  onIdentifierChange,
  onPasswordChange,
  onTogglePassword,
  onSubmit,
}: Props) {
  return (
    <>
      <h2 className="g-title">Welcome Back</h2>
      <p className="g-lead">Enter your credentials to access your secure account</p>
      {error ? <div className="g-alert g-alert-error">{error}</div> : null}
      <form onSubmit={onSubmit}>
        <div className="g-field">
          <label htmlFor="si-identifier">Email or username</label>
          <div className="g-input-row">
            <IconMail />
            <input
              id="si-identifier"
              type="text"
              autoComplete="username"
              required
              placeholder="name@domain.com or username"
              value={identifier}
              onChange={(e) => onIdentifierChange(e.target.value)}
            />
          </div>
        </div>
        <div className="g-field">
          <label htmlFor="si-password">Password</label>
          <div className="g-input-row">
            <IconLock />
            <input
              id="si-password"
              type={showPassword ? 'text' : 'password'}
              autoComplete="current-password"
              required
              placeholder="••••••••"
              value={password}
              onChange={(e) => onPasswordChange(e.target.value)}
            />
            <button
              type="button"
              className="g-toggle-pw"
              aria-label={showPassword ? 'Hide password' : 'Show password'}
              onClick={onTogglePassword}
            >
              👁
            </button>
          </div>
        </div>
        <div className="g-row-between">
          <span />
          <Link className="g-link" to="/forgot-password">
            Forgot password?
          </Link>
        </div>
        <button className="g-btn-primary" type="submit" disabled={pending}>
          {pending ? 'Signing in…' : 'Sign In →'}
        </button>
      </form>
    </>
  )
}
