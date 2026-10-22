import { useEffect, type ReactNode } from 'react'
import { useAuth } from '../lib/auth'
import { DRIVER_PATH, HOME_PATH, replace, usePathname } from '../lib/route'
import { IconLayers } from './Icons'
import DriverView from './DriverView'
import Login from './Login'
import ThemeToggle from './ThemeToggle'
import { ROLE_DRIVER } from '../lib/session'

/**
 * Protected route: the map and everything under it only mount for a signed-in
 * user. Signed out, the same gate serves the sign-in page. Accounts are created
 * by an admin, so there is no public registration screen.
 *
 * A driver never sees the dashboard: the gate sends the driver to the screen
 * made for the road, and keeps the address bar in step with it.
 */
export default function AuthGate({ children }: { children: ReactNode }) {
  const { user, role } = useAuth()
  const path = usePathname()

  const isDriver = !!user && role === ROLE_DRIVER

  // The path and the role have to agree. A driver who opens the dashboard URL
  // is sent to /driver, and anybody else who opens /driver goes back to the
  // map. The sign-in already puts the path right, so this only catches a
  // hand-typed URL or the back button.
  useEffect(() => {
    if (!user) return
    if (isDriver && path !== DRIVER_PATH) replace(DRIVER_PATH)
    if (!isDriver && path === DRIVER_PATH) replace(HOME_PATH)
  }, [user, isDriver, path])

  if (!user) {
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

  if (isDriver) return <DriverView />

  return <>{children}</>
}
