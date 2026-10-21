/** React AuthContext — provides user session state and login/logout actions. */

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api } from './api'
import {
  clearToken,
  decodeUser,
  getToken,
  normalizeRole,
  onSessionInvalid,
  setToken,
  ROLE_ADMIN,
  ROLE_DRIVER,
  ROLE_VIEWER,
  type Role,
} from './session'

export interface AuthUser {
  id: number
  username: string
  email: string
  /** Display name from the account profile, when the admin filled it in. */
  name?: string
  role: Role
  created_at?: string
}

interface AuthContextValue {
  user: AuthUser | null
  /** Role of the signed-in user; 'viewer' while there is no session. */
  role: Role
  /** True for admins — the only role allowed to write data. */
  isAdmin(): boolean
  /** True for the read-only 'viewer' role. */
  isViewer(): boolean
  /** True for the 'driver' role (read-only in this stage). */
  isDriver(): boolean
  /** Shorthand for isAdmin(), for components that only gate write actions. */
  canWrite(): boolean
  login(identifier: string, password: string): Promise<void>
  logout(): void
}

const AuthContext = createContext<AuthContextValue | null>(null)

/** Hook to access the auth context. Must be used within AuthProvider. */
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}

/** Reads the stored token and decodes it, or null when there is no usable session. */
function restoreSession(): AuthUser | null {
  const stored = getToken()
  if (!stored) return null
  const decoded = decodeUser(stored)
  if (!decoded) {
    clearToken()
    return null
  }
  return decoded
}

/** Provides auth state to the app. Wraps the root component. */
export function AuthProvider({ children }: { children: ReactNode }) {
  // Restored synchronously so a reload does not flash the login screen first.
  const [user, setUser] = useState<AuthUser | null>(restoreSession)

  // A rejected token (expired or revoked) drops the session and shows login.
  useEffect(() => onSessionInvalid(() => setUser(null)), [])

  const login = useCallback(async (identifier: string, password: string) => {
    const res = await api.login(identifier, password)
    setToken(res.token)
    setUser({ ...res.user, role: normalizeRole(res.user.role) })
  }, [])

  const logout = useCallback(() => {
    clearToken()
    setUser(null)
  }, [])

  const value = useMemo<AuthContextValue>(() => {
    const role = user?.role ?? ROLE_VIEWER
    return {
      user,
      role,
      isAdmin: () => role === ROLE_ADMIN,
      isViewer: () => role === ROLE_VIEWER,
      isDriver: () => role === ROLE_DRIVER,
      canWrite: () => role === ROLE_ADMIN,
      login,
      logout,
    }
  }, [user, login, logout])

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}