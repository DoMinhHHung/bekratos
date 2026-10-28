import { useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { authApi } from '../api/client'
import { useAuth } from '../lib/auth-context'

const navItems = [
  { to: '/app', end: true, label: 'Overview', icon: 'grid' },
  { to: '/app/orgs', end: false, label: 'Organizations', icon: 'building' },
  { to: '/app/account', end: false, label: 'Account', icon: 'user' },
]

function NavIcon({ name }: { name: string }) {
  const icons: Record<string, JSX.Element> = {
    grid: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3" y="3" width="7" height="7" rx="1" />
        <rect x="14" y="3" width="7" height="7" rx="1" />
        <rect x="14" y="14" width="7" height="7" rx="1" />
        <rect x="3" y="14" width="7" height="7" rx="1" />
      </svg>
    ),
    building: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M3 21h18" />
        <path d="M5 21V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v16" />
        <path d="M13 21V9a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v12" />
        <path d="M8 7v.01M8 11v.01M8 15v.01" />
      </svg>
    ),
    user: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="8" r="4" />
        <path d="M4 21c0-4 4-6 8-6s8 2 8 6" />
      </svg>
    ),
    logout: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
        <polyline points="16 17 21 12 16 7" />
        <line x1="21" y1="12" x2="9" y2="12" />
      </svg>
    ),
    menu: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <line x1="3" y1="6" x2="21" y2="6" />
        <line x1="3" y1="12" x2="21" y2="12" />
        <line x1="3" y1="18" x2="21" y2="18" />
      </svg>
    ),
  }
  return icons[name] || null
}

function getActivePage(pathname: string): { title: string; subtitle: string } {
  if (pathname === '/app') return { title: 'Overview', subtitle: 'Session and service status' }
  if (pathname.startsWith('/app/account')) return { title: 'Account', subtitle: 'Profile and security settings' }
  if (pathname.startsWith('/app/orgs')) return { title: 'Organizations', subtitle: 'Manage teams, projects, and API keys' }
  return { title: 'Console', subtitle: '' }
}

export function Shell() {
  const { user, setUser } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const [sidebarOpen, setSidebarOpen] = useState(false)

  async function logout() {
    await authApi.logout()
    setUser(null)
    navigate('/login')
  }

  const initial = (user?.fullName || user?.email || '?').charAt(0).toUpperCase()
  const { title, subtitle } = getActivePage(location.pathname)

  return (
    <div className="app-shell">
      <aside className={`sidebar ${sidebarOpen ? 'is-open' : ''}`}>
        <div className="sidebar-header">
          <div className="sidebar-brand">
            <div className="brand-mark">P</div>
            <div className="brand-text">Pizza</div>
          </div>
        </div>
        <nav className="sidebar-nav">
          <div className="sidebar-label">Workspace</div>
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) => `sidebar-link ${isActive ? 'active' : ''}`}
              onClick={() => setSidebarOpen(false)}
            >
              <NavIcon name={item.icon} />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-footer">
          <div className="sidebar-user">
            <div className="avatar">{initial}</div>
            <div className="sidebar-user-info">
              <div className="sidebar-user-name">{user?.fullName || 'User'}</div>
              <div className="sidebar-user-email">{user?.email}</div>
            </div>
          </div>
          <button type="button" className="logout-btn" onClick={logout}>
            <NavIcon name="logout" />
            Log out
          </button>
        </div>
      </aside>
      <div
        className={`sidebar-overlay ${sidebarOpen ? 'is-visible' : ''}`}
        onClick={() => setSidebarOpen(false)}
        aria-hidden="true"
      />
      <div className="main-area">
        <header className="topbar">
          <button
            type="button"
            className="menu-toggle"
            onClick={() => setSidebarOpen(true)}
            aria-label="Open menu"
          >
            <NavIcon name="menu" />
          </button>
          <div style={{ minWidth: 0, flex: 1 }}>
            <div className="topbar-title">{title}</div>
            <div className="topbar-subtitle">{subtitle}</div>
          </div>
        </header>
        <main className="main">
          <div className="fade-in">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
