/**
 * The two screens of the app, addressed by the path in the address bar.
 *
 * There is no router library: the path *is* the route. The driver gets
 * /driver (the screen made for the road), everybody else gets / (the map
 * with the panels).
 */

import { useEffect, useState } from 'react'

/** The map with the panels: home for admins and viewers. */
export const HOME_PATH = '/'

/** The driver screen: full-screen map, the unit and the trip. */
export const DRIVER_PATH = '/driver'

/** Fired when the app changes the path itself (the back button does not). */
const NAVIGATE_EVENT = 'groundwork:navigate'

/** The path the app is on right now. */
export function currentPath(): string {
  if (typeof window === 'undefined') return HOME_PATH
  return window.location.pathname
}

/**
 * Go to another screen without reloading the page.
 *
 * The path is pushed, so the back button returns to the previous screen.
 */
export function navigate(path: string): void {
  if (typeof window === 'undefined') return
  if (window.location.pathname === path) return
  window.history.pushState({}, '', path)
  window.dispatchEvent(new Event(NAVIGATE_EVENT))
}

/**
 * Go to another screen and forget where the user was.
 *
 * Used for signing out: the sign-in screen must not be one back-button press
 * away from the dashboard.
 */
export function replace(path: string): void {
  if (typeof window === 'undefined') return
  if (window.location.pathname === path) return
  window.history.replaceState({}, '', path)
  window.dispatchEvent(new Event(NAVIGATE_EVENT))
}

/**
 * The path the app is on, kept in step with the address bar.
 *
 * Both the app itself (which pushes a path) and the browser (the back
 * button) are listened to, so the screen always matches the address.
 */
export function usePathname(): string {
  const [path, setPath] = useState(currentPath)

  useEffect(() => {
    const sync = () => setPath(currentPath())
    window.addEventListener('popstate', sync)
    window.addEventListener(NAVIGATE_EVENT, sync)
    return () => {
      window.removeEventListener('popstate', sync)
      window.removeEventListener(NAVIGATE_EVENT, sync)
    }
  }, [])

  return path
}
