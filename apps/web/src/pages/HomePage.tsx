import { Link } from 'react-router-dom'
import { isLoggedIn } from '../lib/auth-storage'

export function HomePage() {
  const inSession = isLoggedIn()
  return (
    <div className="g-auth-page">
      <div className="g-bg" aria-hidden="true" />
      <div className="g-stage">
        <div className="g-brand">
          <h1>
            Bekratos
            <span>Console</span>
          </h1>
        </div>
        <div className="g-simple-card">
          <h2 className="g-title">Backend infrastructure console</h2>
          <p className="g-lead">
            Sign in against auth-service. Manage organizations, projects, and API keys via
            control-plane.
          </p>
          {inSession ? (
            <Link className="g-btn-primary" to="/app" style={{ textDecoration: 'none' }}>
              Open console →
            </Link>
          ) : (
            <div style={{ display: 'grid', gap: 10 }}>
              <Link className="g-btn-primary" to="/login" style={{ textDecoration: 'none' }}>
                Sign in →
              </Link>
              <Link
                to="/register"
                className="g-link"
                style={{ textAlign: 'center', display: 'block', marginTop: 8 }}
              >
                Create an account
              </Link>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
