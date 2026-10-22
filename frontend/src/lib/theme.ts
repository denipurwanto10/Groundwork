/** Shared light/dark theme store, used by the login screen and the app. */
import { useSyncExternalStore } from 'react'

export type Theme = 'light' | 'dark'

const THEME_KEY = 'entity-manager:theme'
const listeners = new Set<() => void>()

function readInitial(): Theme {
  try {
    const saved = localStorage.getItem(THEME_KEY)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    /* storage unavailable */
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

let current: Theme = readInitial()

function apply(theme: Theme) {
  document.documentElement.dataset.theme = theme
  document.documentElement.style.colorScheme = theme
}

// Applied at import time so the login page never flashes the wrong theme.
apply(current)

function subscribe(fn: () => void) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

let themeTimer: number | undefined

export function setTheme(next: Theme) {
  current = next
  // A short-lived class lets colours cross-fade instead of snapping.
  const root = document.documentElement
  root.classList.add('theme-switching')
  window.clearTimeout(themeTimer)
  themeTimer = window.setTimeout(() => root.classList.remove('theme-switching'), 450)
  apply(next)
  try {
    localStorage.setItem(THEME_KEY, next)
  } catch {
    /* storage unavailable */
  }
  listeners.forEach((fn) => fn())
}

export function useTheme() {
  const theme = useSyncExternalStore(subscribe, () => current)
  return { theme, toggleTheme: () => setTheme(theme === 'dark' ? 'light' : 'dark') }
}
