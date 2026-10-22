/**
 * Offline support for the driver screen.
 *
 * A driver is on the road, and the road has dead spots. Two things have to
 * keep working there:
 *
 * - The **map**: the tiles that were on screen are kept by the service
 *   worker (see `vite.config.ts`), so the road stays visible.
 * - The **trip**: the route the dispatcher planned is copied into
 *   localStorage, so the driver still sees where the trip goes.
 *
 * Nothing here talks to the network: the browser already knows whether it
 * has a connection (`navigator.onLine`), and the trip is read straight from
 * the storage of the device.
 */

import { useEffect, useRef, useState } from 'react'
import type { Vehicle, VehicleRoute } from './types'

/**
 * Where the copy of the trip is kept.
 *
 * The key carries the id of the driver: a phone is sometimes shared, and the
 * trip of one driver must not show up under the name of the next one.
 */
const SNAPSHOT_KEY = 'groundwork:driver:snapshot'

/** The copy of every driver, in memory. `undefined` means "not read yet". */
const cache = new Map<string, DriverSnapshot | null>()

/** How many trips of today are kept with the snapshot. */
const HISTORY_KEEP = 20

/**
 * How often the copy is written.
 *
 * The position of the unit changes several times a second, and localStorage
 * is not cheap. Writing every few seconds is enough to keep the copy fresh:
 * the trip itself changes far less often than the position.
 */
const WRITE_INTERVAL_MS = 3000

/**
 * The copy of the driver's screen: the unit, the trip and the trips of
 * today, as they were the last time the app had a connection.
 */
export interface DriverSnapshot {
  /** When the copy was written. */
  saved_at: string
  /** The unit the driver was on. */
  vehicle: Vehicle | null
  /** The trip on screen. */
  route: VehicleRoute | null
  /** The trips of today. */
  history: VehicleRoute[]
}

/**
 * True when the browser believes it has a connection.
 *
 * The browser only knows what the operating system says; a server that is
 * down is a different problem, and the API reports that on its own.
 */
export function isOnline(): boolean {
  if (typeof navigator === 'undefined') return true
  return navigator.onLine
}

/**
 * The connection state of the browser, kept in step with the network.
 *
 * The browser fires `online` and `offline` when the connection changes. The
 * state is read once more when the tab comes back to the front: a phone that
 * was asleep in a dead spot wakes up with a stale value.
 */
export function useOnline(): boolean {
  const [online, setOnline] = useState(isOnline)

  useEffect(() => {
    const goOnline = () => setOnline(true)
    const goOffline = () => setOnline(false)
    const onVisible = () => {
      if (document.visibilityState === 'visible') setOnline(navigator.onLine)
    }

    window.addEventListener('online', goOnline)
    window.addEventListener('offline', goOffline)
    document.addEventListener('visibilitychange', onVisible)

    return () => {
      window.removeEventListener('online', goOnline)
      window.removeEventListener('offline', goOffline)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [])

  return online
}

/** The storage key of the copy of one driver. */
function snapshotKey(userId: number): string {
  return `${SNAPSHOT_KEY}:${userId}`
}

/**
 * Write the copy of the trip.
 *
 * The storage can be full or blocked (private mode); a copy that cannot be
 * written must not break the screen, so the error is swallowed on purpose.
 */
export function saveDriverSnapshot(userId: number, snapshot: Omit<DriverSnapshot, 'saved_at'>): void {
  const payload: DriverSnapshot = {
    saved_at: new Date().toISOString(),
    vehicle: snapshot.vehicle,
    route: snapshot.route,
    history: snapshot.history.slice(0, HISTORY_KEEP),
  }
  cache.set(snapshotKey(userId), payload)
  try {
    localStorage.setItem(snapshotKey(userId), JSON.stringify(payload))
  } catch {
    /* storage unavailable: the trip is only in memory then */
  }
}

/**
 * The copy of the trip, or null when there is none (or it is broken).
 *
 * The copy is written by this app and read by it again, but the storage can
 * be edited by hand and can hold an old shape, so everything is checked
 * before it is used.
 *
 * The answer is kept in memory as well: the screen asks for it while it
 * mounts, and the storage should not be read again for every question.
 */
export function loadDriverSnapshot(userId: number): DriverSnapshot | null {
  const key = snapshotKey(userId)
  const known = cache.get(key)
  if (known !== undefined) return known
  const read = readDriverSnapshot(key)
  cache.set(key, read)
  return read
}

/** Read the copy straight from the storage. */
function readDriverSnapshot(key: string): DriverSnapshot | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<DriverSnapshot>
    if (!parsed || typeof parsed !== 'object') return null
    return {
      saved_at: typeof parsed.saved_at === 'string' ? parsed.saved_at : '',
      vehicle: parsed.vehicle ?? null,
      route: parsed.route ?? null,
      history: Array.isArray(parsed.history) ? parsed.history : [],
    }
  } catch {
    return null
  }
}

/** Forget the copy of the trip of one driver. */
export function clearDriverSnapshot(userId: number): void {
  const key = snapshotKey(userId)
  cache.set(key, null)
  try {
    localStorage.removeItem(key)
  } catch {
    /* ignore */
  }
}

/**
 * Keeps a copy of the trip in localStorage while the screen is open.
 *
 * The write is throttled: the position of the unit arrives several times a
 * second, and the storage is written at most every few seconds. The last
 * change is written when the screen goes away, so closing the tab never
 * loses the trip.
 */
export function useDriverSnapshot(
  userId: number | null,
  snapshot: Omit<DriverSnapshot, 'saved_at'>,
): void {
  const latest = useRef(snapshot)
  const timer = useRef<number | null>(null)

  useEffect(() => {
    latest.current = snapshot
    // A write is already on its way: it will pick up the newest state.
    if (timer.current !== null) return
    timer.current = window.setTimeout(() => {
      timer.current = null
      if (userId != null) saveDriverSnapshot(userId, latest.current)
    }, WRITE_INTERVAL_MS)
  }, [snapshot, userId])

  useEffect(
    () => () => {
      if (timer.current !== null) {
        window.clearTimeout(timer.current)
        timer.current = null
      }
      if (userId != null) saveDriverSnapshot(userId, latest.current)
    },
    [userId],
  )
}
