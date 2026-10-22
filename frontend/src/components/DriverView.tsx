import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from '../lib/api'
import type { RouteStop, Task, TaskStatus, Vehicle, VehicleRoute } from '../lib/types'
import {
  currentRouteStop,
  formatDistance,
  formatDuration,
  formatEtaClock,
  formatLastSeen,
  routeEtaAt,
  routeStopStatusLabel,
  vehicleKindLabel,
  vehicleStatusLabel,
} from '../lib/types'
import { useAuth } from '../lib/auth'
import { useTheme } from '../lib/theme'
import { isOnline, loadDriverSnapshot, useDriverSnapshot, useOnline } from '../lib/offline'
import { openVehicleStream, type StreamState } from '../lib/vehicleStream'
import DriverMap from './DriverMap'
import { TaskList } from './TaskBoard'
import { IconAlert, IconCheck, IconClipboard, IconClock, IconLogout, IconMoon, IconNavigation, IconPlay, IconRoute, IconSun } from './Icons'

/** Re-renders every few seconds so "12s ago" and the ETA keep moving. */
function useNow(intervalMs = 5000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs)
    return () => window.clearInterval(id)
  }, [intervalMs])
  return now
}

/** "14:32" — the wall clock of a moment, in the timezone of the phone. */
function clockOf(iso: string | null | undefined): string {
  if (!iso) return ''
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

/**
 * What the chip of a stop says: the status, and when it happened.
 *
 * The times are the ones of the server, so two drivers of the same unit read
 * the same thing.
 */
function stopStatusText(stop: RouteStop): string {
  const label = routeStopStatusLabel(stop.status)
  if (stop.status === 'arrived') {
    const at = clockOf(stop.arrived_at)
    return at ? `${label} · ${at}` : label
  }
  if (stop.status === 'completed') {
    const at = clockOf(stop.completed_at)
    return at ? `${label} · ${at}` : label
  }
  return label
}

/** True when the moment falls on the day of the clock. */
function isToday(iso: string, now: number): boolean {
  const moment = new Date(iso)
  if (Number.isNaN(moment.getTime())) return false
  const today = new Date(now)
  return (
    moment.getFullYear() === today.getFullYear() &&
    moment.getMonth() === today.getMonth() &&
    moment.getDate() === today.getDate()
  )
}

/**
 * The driver screen.
 *
 * A driver is on the road with the phone in one hand, so this screen is
 * built for a small display: the map fills it, the trip sits in a card at the
 * bottom, and the one button that matters — start the trip — is as big as a
 * thumb. Nothing else is on the way.
 */
export default function DriverView() {
  const { user, logout } = useAuth()
  const { theme, toggleTheme } = useTheme()

  // The id of the driver: the copy of the trip on the device belongs to
  // this account only (a phone is sometimes shared).
  const userId = user?.id ?? 0

  // The screen starts from the copy on the device: a driver who opens the
  // app in a dead spot must see the trip at once, not a spinner. The answer
  // of the server takes over as soon as it arrives.
  const [vehicles, setVehicles] = useState<Vehicle[]>(() => {
    const stored = loadDriverSnapshot(userId)
    return stored?.vehicle ? [stored.vehicle] : []
  })
  const [selectedId, setSelectedId] = useState<number | null>(
    () => loadDriverSnapshot(userId)?.vehicle?.id ?? null,
  )
  /** The routes the unit already drove (the history of today). */
  const [history, setHistory] = useState<VehicleRoute[]>(() => loadDriverSnapshot(userId)?.history ?? [])
  /** The route the unit is driving right now, pushed over the live feed. */
  const [liveRoute, setLiveRoute] = useState<VehicleRoute | null>(
    () => loadDriverSnapshot(userId)?.route ?? null,
  )

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'ok' | 'bad'; text: string } | null>(null)
  const [starting, setStarting] = useState(false)
  const [streamState, setStreamState] = useState<StreamState>('connecting')
  const [expanded, setExpanded] = useState(true)
  const now = useNow(5000)

  // The work orders of this driver. The server only hands out the tasks that
  // are assigned to the signed-in account, so this list is the driver's own
  // work.
  const [tasks, setTasks] = useState<Task[]>([])
  const [tasksLoading, setTasksLoading] = useState(true)
  const [tasksError, setTasksError] = useState<string | null>(null)
  const [taskBusyId, setTaskBusyId] = useState<number | null>(null)

  // The stops of the route: every one of them has a status of its own, and
  // the one the unit is serving is the one the driver can finish.
  //
  // The note belongs to the stop it was written for: the driver moves on to
  // the next stop, and the note of the stop before it stays with it.
  const [stopNote, setStopNote] = useState({ id: 0, text: '' })
  /** The stop that is being finished right now. */
  const [stopBusyId, setStopBusyId] = useState<number | null>(null)

  // Is there a connection at all? The road has dead spots, and there the
  // trip comes from the copy on the device.
  const online = useOnline()

  // The units of this driver. The API answers with the units assigned to
  // the signed-in account, so a driver never sees anybody else's fleet.
  useEffect(() => {
    let alive = true
    api
      .listVehicles()
      .then((list) => {
        if (!alive) return
        setVehicles(list.vehicles)
        setSelectedId((prev) => prev ?? list.vehicles[0]?.id ?? null)
        setError(null)
      })
      .catch((err) => {
        if (!alive) return
        // No connection: the copy on the device is what the driver gets, so
        // there is nothing to report.
        if (loadDriverSnapshot(userId) || !isOnline()) return
        setError(err instanceof Error ? err.message : 'Could not load your unit.')
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [userId])

  // The unit on screen, remembered for the live feed: the handler of a
  // frame is written once, when the feed opens.
  const selectedIdRef = useRef(selectedId)
  useEffect(() => {
    selectedIdRef.current = selectedId
  }, [selectedId])

  /**
   * The active route, asked for again.
   *
   * The stops of a route change while the unit drives it: the server detects
   * the arrival, and the driver confirms the stops. The card follows the
   * server instead of guessing.
   */
  const reloadRoute = useCallback(async (vehicleId: number) => {
    try {
      setLiveRoute(await api.getVehicleRoute(vehicleId))
    } catch (err) {
      // No active route any more: the trip is over, or it was cleared.
      if (err instanceof ApiError && err.status === 404) setLiveRoute(null)
    }
  }, [])

  // The live feed: the position of the unit and the route it drives.
  useEffect(
    () =>
      openVehicleStream({
        onFrame: (frame) => {
          if (frame.type === 'snapshot') {
            setVehicles(frame.vehicles)
            return
          }
          if (frame.type === 'position' || frame.type === 'vehicle_updated') {
            setVehicles((prev) => {
              const at = prev.findIndex((v) => v.id === frame.vehicle.id)
              if (at === -1) return [...prev, frame.vehicle]
              const next = prev.slice()
              next[at] = frame.vehicle
              return next
            })
            return
          }
          if (frame.type === 'vehicle_deleted') {
            setVehicles((prev) => prev.filter((v) => v.id !== frame.id))
            return
          }
          if (frame.type === 'route') {
            setLiveRoute(frame.route)
            return
          }
          if (frame.type === 'route_stop') {
            // A stop of the route changed: the route is asked for again, so
            // the status of the stops is on screen without a reload.
            const unit = selectedIdRef.current
            if (unit != null && frame.vehicle_id === unit) {
              void reloadRoute(unit)
            }
            return
          }
          if (frame.type === 'route_cleared' || frame.type === 'route_completed') {
            setLiveRoute((prev) => (prev && prev.id === frame.route_id ? null : prev))
          }
        },
        onState: setStreamState,
      }),
    [reloadRoute],
  )

  // The routes of the unit on screen: the trips of today. The list is
  // asked for again when the connection comes back, so the trips of the
  // server replace the copy on the device.
  useEffect(() => {
    if (selectedId == null) return
    let alive = true
    api
      .listVehicleRoutes(selectedId)
      .then((data) => {
        if (alive) setHistory(data.routes)
      })
      .catch(() => {
        /* the list stays as it is; the live feed still reports the trip */
      })
    return () => {
      alive = false
    }
  }, [selectedId, online])

  // The work orders of this driver. The server only hands out the tasks that
  // are assigned to the signed-in account, so this list is the driver's own
  // work — nothing to filter out here.
  const refreshTasks = useCallback(async () => {
    setTasksLoading(true)
    try {
      setTasks(await api.listTasks())
      setTasksError(null)
    } catch (err) {
      setTasksError(err instanceof Error ? err.message : 'Could not load your tasks.')
    } finally {
      setTasksLoading(false)
    }
  }, [])

  useEffect(() => {
    void refreshTasks()
  }, [refreshTasks])

  /** Close a job: the driver says it is done. */
  const completeTask = useCallback(
    async (id: number, status: TaskStatus) => {
      setTaskBusyId(id)
      try {
        const updated = await api.updateTaskStatus(id, status)
        setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)))
        setNotice({ kind: 'ok', text: `"${updated.title}" is done.` })
      } catch (err) {
        setNotice({
          kind: 'bad',
          text: err instanceof Error ? err.message : 'Could not save the task.',
        })
      } finally {
        setTaskBusyId(null)
      }
    },
    [],
  )

  // The connection is back: the fleet is asked for again, because what is on
  // screen is what was known when the signal died.
  const wasOnline = useRef(online)
  useEffect(() => {
    if (wasOnline.current === online) return
    wasOnline.current = online
    if (!online) return
    void api
      .listVehicles()
      .then((list) => {
        setVehicles(list.vehicles)
        setSelectedId((prev) => prev ?? list.vehicles[0]?.id ?? null)
        setError(null)
      })
      .catch(() => {
        /* the stored trip stays on screen */
      })
  }, [online])

  const vehicle = useMemo(
    () => vehicles.find((v) => v.id === selectedId) ?? vehicles[0],
    [vehicles, selectedId],
  )

  // The trip on screen: the live one, or the one the unit already carries.
  const route = useMemo(() => {
    if (liveRoute && liveRoute.vehicle_id === vehicle?.id) return liveRoute
    if (vehicle?.active_route) return vehicle.active_route
    return history.find((r) => r.status === 'active') ?? null
  }, [liveRoute, vehicle, history])

  const todaysRoutes = useMemo(
    () => history.filter((r) => isToday(r.created_at, now)),
    [history, now],
  )

  // The stops of the route, in the visiting order the server worked out, and
  // the one the unit is serving right now: the stops are visited one after
  // the other, so only that one can be finished.
  const routeStops = useMemo(() => route?.stops ?? [], [route])
  const activeStop = useMemo(() => currentRouteStop(routeStops), [routeStops])
  /** The note of the stop the driver is serving, if it was written for it. */
  const note = activeStop && stopNote.id === activeStop.id ? stopNote.text : ''

  const started = !!route?.started_at
  // Starting a trip needs the server: without a connection the button says
  // so instead of failing with an error.
  const canStart = !!vehicle && !!route && !started && !starting && online

  // The trip is copied into localStorage, so the driver keeps it — and the
  // map keeps its tiles — when the signal goes.
  useDriverSnapshot(userId, {
    vehicle: vehicle ?? null,
    route: route ?? null,
    history,
  })

  const startTrip = useCallback(async () => {
    if (!vehicle || !route) return
    setStarting(true)
    setNotice(null)
    try {
      const startedRoute = await api.startVehicleRoute(vehicle.id)
      setLiveRoute(startedRoute)
      setNotice({ kind: 'ok', text: 'Trip started. Follow the line on the map.' })
      setExpanded(true)
    } catch (err) {
      setNotice({
        kind: 'bad',
        text: err instanceof Error ? err.message : 'Could not start the trip.',
      })
    } finally {
      setStarting(false)
    }
  }, [vehicle, route])

  /**
   * Finish the stop the unit is serving: confirmed with a note, or passed
   * by.
   *
   * Finishing a stop needs the server — it is the server that writes down
   * who finished it, when, and where the unit stood — so the buttons are off
   * when the phone has no connection.
   */
  const finishStop = useCallback(
    async (stop: RouteStop, action: 'complete' | 'skip') => {
      if (!vehicle) return
      setStopBusyId(stop.id)
      try {
        const updated =
          action === 'complete'
            ? await api.completeRouteStop(vehicle.id, stop.id, note)
            : await api.skipRouteStop(vehicle.id, stop.id, note)
        setStopNote({ id: 0, text: '' })
        setNotice({
          kind: 'ok',
          text: action === 'complete' ? `"${updated.name}" is done.` : `"${updated.name}" is skipped.`,
        })
        // The next stop is the one the unit is serving now.
        await reloadRoute(vehicle.id)
      } catch (err) {
        setNotice({
          kind: 'bad',
          text: err instanceof Error ? err.message : 'Could not save the stop.',
        })
      } finally {
        setStopBusyId(null)
      }
    },
    [vehicle, note, reloadRoute],
  )

  const name = user?.name?.trim() || user?.username || user?.email || 'Driver'
  const initial = name.charAt(0).toUpperCase()
  const streamLabel = !online
    ? 'Offline'
    : streamState === 'live'
      ? 'Live'
      : streamState === 'connecting'
        ? 'Connecting'
        : 'Reconnecting'

  return (
    <div className="drv">
      <header className="drv-top">
        <span className="drv-avatar" aria-hidden="true">
          {initial}
        </span>
        <span className="drv-who">
          <b>{name}</b>
          <em>Driver</em>
        </span>
        <span className={`drv-live ${online ? streamState : 'offline'}`} title="Live feed">
          <i aria-hidden="true" />
          {streamLabel}
        </span>
        <button
          type="button"
          className="icon-btn"
          onClick={toggleTheme}
          aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
          title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
        >
          {theme === 'dark' ? <IconSun /> : <IconMoon />}
        </button>
        <button className="icon-btn" onClick={logout} aria-label="Sign out" title="Sign out">
          <IconLogout />
        </button>
      </header>

      <div className={`drv-canvas${online ? '' : ' offline'}`}>
        <DriverMap vehicle={vehicle} route={route} theme={theme} />

        {/* No connection: the driver gets the trip from the device, and the
            map from the tiles the worker kept. */}
        {!online && (
          <p className="drv-offline" role="status">
            <IconAlert width={15} height={15} />
            <span>Anda sedang Offline. Rute disimpan di memori.</span>
          </p>
        )}

        {loading && (
          <div className="drv-note">
            <IconClock width={16} height={16} />
            <span>Loading your unit…</span>
          </div>
        )}

        {error && (
          <div className="drv-note bad" role="alert">
            <IconAlert width={16} height={16} />
            <span>{error}</span>
          </div>
        )}

        {!loading && !error && !vehicle && (
          <div className="drv-note">
            <IconAlert width={16} height={16} />
            <span>No unit is assigned to you yet. Ask the dispatcher.</span>
          </div>
        )}
      </div>

      <section className={`drv-sheet${expanded ? ' open' : ''}`} aria-label="Trip">
        <button
          type="button"
          className="drv-handle"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          aria-label={expanded ? 'Collapse the trip card' : 'Expand the trip card'}
        >
          <i aria-hidden="true" />
        </button>

        {/* The collapsed sheet: one line with the trip and the button, so the
            map gets the whole screen when the driver wants to see the road. */}
        <div className="drv-compact">
          <IconNavigation width={18} height={18} />
          <span className="drv-compact-text">
            <b>{route?.destination.name || 'No trip'}</b>
            <em>{route ? `${formatDistance(route.remaining_m)} left` : 'waiting for a plan'}</em>
          </span>
          <button
            type="button"
            className={`drv-start small${started ? ' running' : ''}`}
            onClick={() => void startTrip()}
            disabled={!canStart}
          >
            {started ? 'Running' : !online ? 'Offline' : starting ? 'Starting…' : 'Start'}
          </button>
        </div>

        <div className="drv-card">
          {vehicles.length > 1 && (
            <div className="drv-units" role="group" aria-label="Your units">
              {vehicles.map((v) => (
                <button
                  key={v.id}
                  type="button"
                  className={`drv-unit${v.id === vehicle?.id ? ' on' : ''}`}
                  onClick={() => setSelectedId(v.id)}
                  aria-pressed={v.id === vehicle?.id}
                >
                  {v.code}
                </button>
              ))}
            </div>
          )}

          {/* The unit the driver is on */}
          <div className="drv-block drv-panel">
            <span className="drv-label">Your unit</span>
            {vehicle ? (
              <>
                <div className="drv-unit-head">
                  <h1>{vehicle.name}</h1>
                  <span className={`drv-status ${vehicle.status}`}>{vehicleStatusLabel(vehicle.status)}</span>
                </div>
                <dl className="drv-facts drv-tiles">
                  <div>
                    <dt>Plate</dt>
                    <dd>{vehicle.code}</dd>
                  </div>
                  <div>
                    <dt>Type</dt>
                    <dd>{vehicleKindLabel(vehicle.kind)}</dd>
                  </div>
                  <div>
                    <dt>Speed</dt>
                    <dd>{vehicle.status === 'moving' ? `${Math.round(vehicle.speed_kmh)} km/h` : '—'}</dd>
                  </div>
                  <div>
                    <dt>Updated</dt>
                    <dd>{formatLastSeen(vehicle.last_seen_at, now)}</dd>
                  </div>
                </dl>
              </>
            ) : (
              <p className="drv-empty">No unit assigned.</p>
            )}
          </div>

          {/* The trip on screen */}
          <div className="drv-block drv-panel">
            <span className="drv-label">
              <IconRoute width={14} height={14} /> Current trip
            </span>
            {route ? (
              <>
                <div className="drv-dest">
                  <IconNavigation width={18} height={18} />
                  <div>
                    <b>{route.destination.name || 'Destination'}</b>
                    <span>from {route.origin.name || 'start'}</span>
                  </div>
                </div>
                <dl className="drv-facts drv-tiles drv-stats">
                  <div>
                    <dt>Left</dt>
                    <dd>{formatDistance(route.remaining_m)}</dd>
                  </div>
                  <div>
                    <dt>Total</dt>
                    <dd>{formatDistance(route.distance_m)}</dd>
                  </div>
                  <div>
                    <dt>Drive</dt>
                    <dd>{formatDuration(route.duration_s)}</dd>
                  </div>
                  <div>
                    <dt>ETA</dt>
                    <dd>{formatEtaClock(routeEtaAt(route, vehicle?.speed_kmh ?? 0, now), now)}</dd>
                  </div>
                </dl>
                <div className="drv-progress-row">
                  <span>Progress</span>
                  <b>{Math.round(route.progress * 100)}%</b>
                </div>
                <div
                  className="drv-progress"
                  role="progressbar"
                  aria-label="How much of the trip is behind the unit"
                  aria-valuenow={Math.round(route.progress * 100)}
                  aria-valuemin={0}
                  aria-valuemax={100}
                >
                  <i style={{ width: `${Math.round(route.progress * 100)}%` }} />
                </div>
              </>
            ) : (
              <p className="drv-empty">No trip planned for this unit yet.</p>
            )}
          </div>

          {/* The stops of the route: the trip is a list of steps, and every
              step has a status of its own. The one the unit is serving is
              the one the driver finishes. */}
          {routeStops.length > 0 && (
            <div className="drv-block drv-panel">
              <span className="drv-label">
                <IconRoute width={14} height={14} /> Stops ({routeStops.length})
              </span>
              <ol className="drv-stops">
                {routeStops.map((stop, index) => {
                  const isActive = activeStop?.id === stop.id
                  const busy = stopBusyId === stop.id
                  const arrived = stop.status === 'arrived'
                  return (
                    <li key={stop.id ?? index} className={`drv-stop ${stop.status}${isActive ? ' active' : ''}`}>
                      <div className="drv-stop-head">
                        <b className="drv-stop-num">{index + 1}</b>
                        <span className="drv-stop-name">{stop.name || `Stop ${index + 1}`}</span>
                        <span className={`drv-stop-chip ${stop.status}`}>{stopStatusText(stop)}</span>
                      </div>

                      {stop.completion_note && <p className="drv-stop-note">“{stop.completion_note}”</p>}

                      {isActive && (
                        <div className="drv-stop-actions">
                          {arrived && (
                            <input
                              className="drv-stop-input"
                              value={note}
                              onChange={(e) => setStopNote({ id: stop.id, text: e.target.value })}
                              placeholder="Paket diterima satpam"
                              aria-label={`Note for ${stop.name || `stop ${index + 1}`}`}
                              maxLength={500}
                              disabled={busy || !online}
                            />
                          )}
                          <button
                            type="button"
                            className="drv-stop-done"
                            onClick={() => void finishStop(stop, 'complete')}
                            disabled={!arrived || busy || !online}
                            title={!online ? 'Finishing a stop needs a connection' : undefined}
                          >
                            {busy ? 'Saving…' : 'Complete this stop'}
                          </button>
                          <button
                            type="button"
                            className="drv-stop-skip"
                            onClick={() => void finishStop(stop, 'skip')}
                            disabled={busy || !online}
                            title={!online ? 'Finishing a stop needs a connection' : undefined}
                          >
                            Skip
                          </button>
                        </div>
                      )}
                    </li>
                  )
                })}
              </ol>
            </div>
          )}

          {/* The work orders of this driver: the jobs of the day */}
          <div className="drv-block">
            <span className="drv-label">
              <IconClipboard width={14} height={14} /> Your tasks ({tasks.length})
            </span>
            <TaskList
              tasks={tasks}
              loading={tasksLoading}
              error={tasksError}
              busyId={taskBusyId}
              onStatus={completeTask}
              onRetry={() => void refreshTasks()}
            />
          </div>

          {/* The trips of today */}
          {todaysRoutes.length > 0 && (
            <div className="drv-block">
              <span className="drv-label">Today&apos;s trips ({todaysRoutes.length})</span>
              <ul className="drv-trips">
                {todaysRoutes.map((r) => (
                  <li key={r.id}>
                    <span className={`drv-trip-dot ${r.status}`} aria-hidden="true" />
                    <b>{r.destination.name || 'Destination'}</b>
                    <span className="drv-trip-meta">
                      {formatDistance(r.distance_m)} · {r.status}
                      {r.started_at ? ' · started' : ''}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>

        <div className="drv-foot">
          {notice && (
            <p className={`drv-notice ${notice.kind}`} role="status">
              {notice.kind === 'ok' ? <IconCheck width={14} height={14} /> : <IconAlert width={14} height={14} />}
              {notice.text}
            </p>
          )}

          <button
            type="button"
            className={`drv-start${started ? ' running' : ''}`}
            onClick={() => void startTrip()}
            disabled={!canStart}
            title={!online ? 'Starting a trip needs a connection' : undefined}
          >
            {started ? (
              <>
                <IconCheck width={22} height={22} />
                <span>Trip running</span>
                {route?.started_at && (
                  <em>since {new Date(route.started_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}</em>
                )}
              </>
            ) : !online ? (
              <>
                <IconAlert width={22} height={22} />
                <span>Offline — the trip is saved on the device</span>
              </>
            ) : (
              <>
                <IconPlay width={22} height={22} />
                <span>{starting ? 'Starting…' : 'Start trip'}</span>
              </>
            )}
          </button>
        </div>
      </section>
    </div>
  )
}
