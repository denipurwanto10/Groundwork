/** JWT token persistence (localStorage) and session helpers. */

const TOKEN_KEY = 'groundwork:token'
const REFRESH_KEY = 'groundwork:refresh'

/** Roles the backend accepts. Mirrors the CHECK constraint on users.role. */
export const ROLE_ADMIN = 'admin'
export const ROLE_STAFF = 'staff'
export const ROLE_VIEWER = 'viewer'
export const ROLE_DRIVER = 'driver'

export type Role = typeof ROLE_ADMIN | typeof ROLE_STAFF | typeof ROLE_VIEWER | typeof ROLE_DRIVER

/**
 * Coerce a raw role claim into a known role. Anything the backend does not
 * know about (a stale token from an older role set, for instance) is treated
 * as read-only, so the UI never offers write actions the API would refuse.
 */
export function normalizeRole(raw: string | undefined | null): Role {
  if (raw === ROLE_ADMIN) return ROLE_ADMIN
  if (raw === ROLE_STAFF) return ROLE_STAFF
  if (raw === ROLE_DRIVER) return ROLE_DRIVER
  return ROLE_VIEWER
}

export interface SessionUser {
  id: number
  /** Login handle. Every account has one, unlike the optional email. */
  username: string
  email: string
  role: Role
}

/** Read the stored token, or null if missing. */
export function getToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

/** Store a new token. */
export function setToken(token: string): void {
  try {
    localStorage.setItem(TOKEN_KEY, token)
  } catch {
    /* private mode / storage full — ignore */
  }
}

/** Remove the token. */
export function clearToken(): void {
  try {
    localStorage.removeItem(TOKEN_KEY)
  } catch {
    /* ignore */
  }
}

/**
 * Read the refresh token, or null if missing. It renews the access token
 * when that one expires, so the operator does not have to sign in again
 * every day.
 */
export function getRefreshToken(): string | null {
  try {
    return localStorage.getItem(REFRESH_KEY)
  } catch {
    return null
  }
}

/** Store a refresh token. */
export function setRefreshToken(token: string): void {
  try {
    localStorage.setItem(REFRESH_KEY, token)
  } catch {
    /* private mode / storage full — ignore */
  }
}

/** Remove the refresh token. */
export function clearRefreshToken(): void {
  try {
    localStorage.removeItem(REFRESH_KEY)
  } catch {
    /* ignore */
  }
}

type SessionListener = () => void
const invalidListeners = new Set<SessionListener>()

/** Subscribe to "the stored token was rejected" events. Returns an unsubscribe fn. */
export function onSessionInvalid(fn: SessionListener): () => void {
  invalidListeners.add(fn)
  return () => {
    invalidListeners.delete(fn)
  }
}

/** Tell the app that the token is no longer usable. */
export function notifySessionInvalid(): void {
  invalidListeners.forEach((fn) => fn())
}

/** Decode the JWT payload (no signature verification) to extract user claims.
 *  Returns null if the token is malformed or expired. */
export function decodeUser(token: string): SessionUser | null {
  const parts = token.split('.')
  if (parts.length !== 3) return null
  try {
    // base64url decode with padding
    let payload = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const pad = payload.length % 4
    if (pad) payload += '='.repeat(4 - pad)
    const json = atob(payload)
    const claims = JSON.parse(json) as {
      sub?: string | number
      username?: string
      email?: string
      role?: string
      exp?: number
    }
    if (typeof claims.exp === 'number' && claims.exp * 1000 <= Date.now()) {
      return null
    }
    // Email is optional since accounts can be created with a username only.
    if (!claims.sub) return null
    return {
      id: typeof claims.sub === 'string' ? Number(claims.sub) : claims.sub,
      username: claims.username ?? '',
      email: claims.email ?? '',
      role: normalizeRole(claims.role),
    }
  } catch {
    return null
  }
}