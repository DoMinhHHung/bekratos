import { Navigate, Route, Routes } from 'react-router-dom'
import { Shell } from './components/Shell'
import { useAuth } from './lib/auth-context'
import { AccountPage } from './pages/AccountPage'
import { ForgotPasswordPage } from './pages/ForgotPasswordPage'
import { HomePage } from './pages/HomePage'
import { LoginPage } from './pages/LoginPage'
import { OrgDetailPage } from './pages/OrgDetailPage'
import { OrgsPage } from './pages/OrgsPage'
import { OverviewPage } from './pages/OverviewPage'
import { RegisterPage } from './pages/RegisterPage'
import { ResetPasswordPage } from './pages/ResetPasswordPage'
import { VerifyEmailPage } from './pages/VerifyEmailPage'
import type { ReactNode } from 'react'

function RequireAuth({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) {
    return (
      <div className="g-auth-page">
        <div className="g-bg" aria-hidden="true" />
        <p className="g-lead" style={{ position: 'relative', zIndex: 1 }}>
          Loading session…
        </p>
      </div>
    )
  }
  if (!user) return <Navigate to="/login" replace />
  return children
}

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<HomePage />} />
      <Route path="/login" element={<LoginPage />} />
      <Route path="/register" element={<RegisterPage />} />
      <Route path="/forgot-password" element={<ForgotPasswordPage />} />
      <Route path="/reset-password" element={<ResetPasswordPage />} />
      <Route path="/verify-email" element={<VerifyEmailPage />} />
      <Route
        path="/app"
        element={
          <RequireAuth>
            <Shell />
          </RequireAuth>
        }
      >
        <Route index element={<OverviewPage />} />
        <Route path="account" element={<AccountPage />} />
        <Route path="orgs" element={<OrgsPage />} />
        <Route path="orgs/:orgId" element={<OrgDetailPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
