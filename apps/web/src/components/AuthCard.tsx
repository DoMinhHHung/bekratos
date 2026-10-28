import { Link } from 'react-router-dom'
import type { ReactNode } from 'react'

type Props = {
  title: string
  lead?: string
  children: ReactNode
}

export function AuthCard({ title, lead, children }: Props) {
  return (
    <div className="auth-layout">
      <div className="auth-card">
        <Link to="/" className="brand" style={{ color: 'inherit' }}>
          <div className="brand-mark">P</div>
          <div>
            <div className="brand-text">Pizza</div>
            <div className="brand-sub">Console</div>
          </div>
        </Link>
        <h1>{title}</h1>
        {lead ? <p className="lead">{lead}</p> : null}
        {children}
      </div>
    </div>
  )
}
