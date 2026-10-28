import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

const CHIP_CENTER_OFFSET = 10
const TIMELINE = { core: 900, swap: 1500, release: 1750, done: 2600 }

type Mode = 'signin' | 'signup'

type Props = {
  mode: Mode
  onModeChange: (mode: Mode) => void
  signIn: ReactNode
  signUp: ReactNode
}

function wait(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

export function GlassyAuthShell({ mode, onModeChange, signIn, signUp }: Props) {
  const shellRef = useRef<HTMLDivElement>(null)
  const [busy, setBusy] = useState(false)
  const [animOut, setAnimOut] = useState(false)
  const [animCore, setAnimCore] = useState(false)
  const [displayMode, setDisplayMode] = useState<Mode>(mode)

  useEffect(() => {
    if (!busy && !animOut && mode !== displayMode) {
      setDisplayMode(mode)
    }
  }, [mode, busy, animOut, displayMode])

  const goTo = useCallback(
    async (target: Mode) => {
      if (target === mode || busy) return
      setBusy(true)
      const el = shellRef.current
      if (el) {
        const rect = el.getBoundingClientRect()
        el.style.setProperty('--dx', `${rect.width / 2 - CHIP_CENTER_OFFSET}px`)
        el.style.setProperty('--dy', `${rect.height / 2 - CHIP_CENTER_OFFSET}px`)
      }
      setAnimOut(true)
      await wait(TIMELINE.core)
      setAnimCore(true)
      await wait(TIMELINE.swap - TIMELINE.core)
      setDisplayMode(target)
      onModeChange(target)
      await wait(TIMELINE.release - TIMELINE.swap)
      setAnimCore(false)
      setAnimOut(false)
      await wait(TIMELINE.done - TIMELINE.release)
      setBusy(false)
    },
    [busy, mode, onModeChange],
  )

  const shellClass = ['g-shell', animOut ? 'is-out' : '', animCore ? 'is-core' : '']
    .filter(Boolean)
    .join(' ')

  return (
    <div className="g-auth-page">
      <div className="g-bg" aria-hidden="true" />
      <div className="g-stage">
        <div className="g-brand">
          <Link to="/" style={{ textDecoration: 'none', color: 'inherit' }}>
            <h1>
              Bekratos
              <span>Console</span>
            </h1>
          </Link>
        </div>

        <div className={shellClass} ref={shellRef}>
          <div className="g-chip g-chip-tl" aria-hidden="true">
            <div className="g-chip-face" />
          </div>
          <div className="g-chip g-chip-br" aria-hidden="true">
            <div className="g-chip-face" />
          </div>

          <div className="g-frame">
            <div className="g-ripple g-ripple-1" aria-hidden="true" />
            <div className="g-ripple g-ripple-2" aria-hidden="true" />
            <div className="g-core" aria-hidden="true" />

            <div className="g-layers">
              <div className={`g-layer${displayMode === 'signin' ? ' is-active' : ''}`}>
                {signIn}
                <p className="g-footer">
                  Don&apos;t have an account?{' '}
                  <button type="button" className="g-link" onClick={() => void goTo('signup')} disabled={busy}>
                    Sign up
                  </button>
                </p>
              </div>
              <div className={`g-layer${displayMode === 'signup' ? ' is-active' : ''}`}>
                {signUp}
                <p className="g-footer">
                  Already have an account?{' '}
                  <button type="button" className="g-link" onClick={() => void goTo('signin')} disabled={busy}>
                    Sign in
                  </button>
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

type SimpleProps = {
  title: string
  lead?: string
  children: ReactNode
}

export function GlassySimpleCard({ title, lead, children }: SimpleProps) {
  return (
    <div className="g-auth-page">
      <div className="g-bg" aria-hidden="true" />
      <div className="g-stage">
        <div className="g-brand">
          <Link to="/" style={{ textDecoration: 'none', color: 'inherit' }}>
            <h1>
              Bekratos
              <span>Console</span>
            </h1>
          </Link>
        </div>
        <div className="g-simple-card">
          <h2 className="g-title">{title}</h2>
          {lead ? <p className="g-lead">{lead}</p> : null}
          {children}
        </div>
      </div>
    </div>
  )
}

export function IconMail() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="2" y="4" width="20" height="16" rx="2" />
      <path d="m22 7-10 7L2 7" />
    </svg>
  )
}

export function IconLock() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="3" y="11" width="18" height="11" rx="2" />
      <path d="M7 11V7a5 5 0 0 1 10 0v4" />
    </svg>
  )
}

export function IconUser() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
      <circle cx="12" cy="7" r="4" />
    </svg>
  )
}

export function IconAt() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="12" r="4" />
      <path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-3.92 7.94" />
    </svg>
  )
}
