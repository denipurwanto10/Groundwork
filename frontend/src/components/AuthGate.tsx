import type { ReactNode } from 'react'
import { useAuth } from '../lib/auth'
import { IconLayers } from './Icons'
import Login from './Login'
import ThemeToggle from './ThemeToggle'

/**
 * Protected route: the map and everything under it only mount for a signed-in
 * user. Signed out, the same gate serves the sign-in page. Accounts are created
 * by an admin, so there is no public registration screen.
 */
export default function AuthGate({ children }: { children: ReactNode }) {
  const { user } = useAuth()

  if (user) return <>{children}</>

  return (
    <div className="auth-screen">
      <ThemeToggle className="auth-theme" />
      <div className="auth-card">
        <div className="auth-brand">
          <span className="brand-mark">
            <IconLayers width={20} height={20} />
          </span>
          <div className="auth-brand-text">
            <h1>Groundwork</h1>
            <span>Field operations, mapped</span>
          </div>
        </div>

        <Login />
      </div>
    </div>
  )
}