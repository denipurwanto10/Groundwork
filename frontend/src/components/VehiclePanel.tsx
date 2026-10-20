import { useEffect, useMemo, useState } from 'react'
import type { CSSProperties } from 'react'
import type { StreamState } from '../lib/vehicleStream'
import type { RouteStop, Vehicle, VehicleRoute, VehicleStatus } from '../lib/types'
import {
  formatDistance,
  formatDuration,
  formatLastSeen,
  hasPosition,
  normalizeVehicleKind,
  normalizeVehicleStatus,
  routeForVehicle,
  routeProgress,
  routeEtaSeconds,
  vehicleKindLabel,
  vehicleStatusColor,
  vehicleStatusLabel,
} from '../lib/types'
import {
  IconAlert,
  IconClock,
  IconCrosshair,
  IconEye,
  IconEyeOff,
  IconFit,
  IconGauge,
  IconNavigation,
  IconRefresh,
  IconRoute,
  IconSearch,
  IconTruck,
  IconX,
} from './Icons'

const STREAM_UI: Record<StreamState, { label: string; title: string }> = {
  connecting: { label: 'Connecting', title: 'Opening the live position feed' },
  live: { label: 'Live', title: 'Receiving positions as they are reported' },
  reconnecting: { label: 'Reconnecting', title: 'The feed dropped; reconnecting on its own' },
}

type Filter = 'all' | VehicleStatus

const FILTERS: { key: Filter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'moving', label: 'Moving' },
  { key: 'idle', label: 'Idle' },
  { key: 'offline', label: 'Offline' },
]

interface Props {
  vehicles: Vehicle[]
  selectedId: number | null
  loading: boolean
  error: string | null
  streamState: StreamState
  visible: boolean
  onVisibleChange: (visible: boolean) => void
  onSelect: (id: number | null) => void
  onRetry: () => void
  /** Camera follows the selected unit while it moves. */
  following: boolean
  onFollowChange: (following: boolean) => void
  /** Zoom the map out to show every located unit. */
  onFitFleet: () => void
  /** Delivery routes of the whole fleet (active ones are drawn on the map). */
  routes: VehicleRoute[]
  /** Stops picked on the map for the selected vehicle's next route. */
  draft: { origin: RouteStop | null; destination: RouteStop | null } | null
  /** Which stop the map click will fill, or null when not picking. */
  picking: 'origin' | 'destination' | null
  onPickStart: (stage: 'origin' | 'destination') => void
  onPickCancel: () => void
  onLabelChange: (stage: 'origin' | 'destination', name: string) => void
  onClearDraft: () => void
  onRouteSet: (vehicleId: number, origin: RouteStop, destination: RouteStop) => Promise<void>
  onRouteClear: (vehicleId: number) => Promise<void>
}

/** Re-renders every few seconds so "12s ago" labels keep moving. */
function useNow(intervalMs = 5000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs)
    return () => window.clearInterval(id)
  }, [intervalMs])
  return now
}

/**
 * Live tracking panel: fleet health at a glance (filter tiles), a searchable
 * list of units, and one detail card for the selected unit with follow mode
 * and the delivery route builder.
 */
export default function VehiclePanel({
  vehicles,
  selectedId,
  loading,
  error,
  streamState,
  visible,
  onVisibleChange,
  onSelect,
  onRetry,
  following,
  onFollowChange,
  onFitFleet,
  routes,
  draft,
  picking,
  onPickStart,
  onPickCancel,
  onLabelChange,
  onClearDraft,
  onRouteSet,
  onRouteClear,
}: Props) {
  const now = useNow()
  const stream = STREAM_UI[streamState]
  const selected = vehicles.find((v) => v.id === selectedId) ?? null
  const activeRoute = routeForVehicle(routes, selectedId)

  const [filter, setFilter] = useState<Filter>('all')
  const [query, setQuery] = useState('')
  const [saving, setSaving] = useState(false)
  const [routeError, setRouteError] = useState<string | null>(null)
  const [planning, setPlanning] = useState(false)

  // A route error belongs to one vehicle: switching selection must not show it
  // for a different unit.
  useEffect(() => {
    setRouteError(null)
    setSaving(false)
    setPlanning(false)
  }, [selectedId])

  const counts = useMemo(() => {
    const c = { all: vehicles.length, moving: 0, idle: 0, offline: 0 }
    for (const v of vehicles) c[normalizeVehicleStatus(v.status)] += 1
    return c
  }, [vehicles])

  const routeByVehicle = useMemo(() => {
    const m = new Map<number, VehicleRoute>()
    for (const r of routes) if (r.status === 'active') m.set(r.vehicle_id, r)
    return m
  }, [routes])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    const order: Record<VehicleStatus, number> = { moving: 0, idle: 1, offline: 2 }
    return vehicles
      .filter((v) => {
        if (filter !== 'all' && normalizeVehicleStatus(v.status) !== filter) return false
        if (!q) return true
        return `${v.name} ${v.code} ${vehicleKindLabel(v.kind)}`.toLowerCase().includes(q)
      })
      .sort(
        (a, b) =>
          order[normalizeVehicleStatus(a.status)] - order[normalizeVehicleStatus(b.status)] ||
          a.name.localeCompare(b.name),
      )
  }, [vehicles, filter, query])

  const submitRoute = async () => {
    if (!selected || !draft?.origin || !draft?.destination) return
    setSaving(true)
    setRouteError(null)
    try {
      await onRouteSet(selected.id, draft.origin, draft.destination)
      setPlanning(false)
    } catch (err) {
      setRouteError(err instanceof Error ? err.message : 'Could not build the route')
    } finally {
      setSaving(false)
    }
  }

  const clearRoute = async (vehicleId: number) => {
    setRouteError(null)
    try {
      await onRouteClear(vehicleId)
    } catch (err) {
      setRouteError(err instanceof Error ? err.message : 'Could not clear the route')
    }
  }

  const selectedStatus = selected ? normalizeVehicleStatus(selected.status) : 'offline'
  const showForm = !!selected && !activeRoute && (planning || !!draft?.origin || !!draft?.destination)

  return (
    <section className="lt" aria-label="Live tracking">
      <header className="lt-head">
        <div className="lt-title">
          <h2>Live tracking</h2>
          <span className={`lt-stream ${streamState}`} role="status" title={stream.title}>
            <span className="lt-pulse" aria-hidden="true" />
            {stream.label}
          </span>
        </div>
        <div className="lt-tools">
          <button
            type="button"
            className="icon-btn"
            onClick={onFitFleet}
            disabled={counts.all === 0}
            title="Fit all vehicles on the map"
            aria-label="Fit all vehicles on the map"
          >
            <IconFit width={16} height={16} />
          </button>
          <button
            type="button"
            className={`icon-btn${visible ? ' active' : ''}`}
            onClick={() => onVisibleChange(!visible)}
            aria-pressed={visible}
            title={visible ? 'Hide vehicles on the map' : 'Show vehicles on the map'}
            aria-label={visible ? 'Hide vehicles on the map' : 'Show vehicles on the map'}
          >
            {visible ? <IconEye width={16} height={16} /> : <IconEyeOff width={16} height={16} />}
          </button>
        </div>
      </header>

      <div className="lt-stats" role="group" aria-label="Filter by status">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            className={`lt-stat ${f.key}${filter === f.key ? ' on' : ''}`}
            aria-pressed={filter === f.key}
            onClick={() => setFilter(f.key)}
          >
            <b>{counts[f.key]}</b>
            <span>{f.label}</span>
          </button>
        ))}
      </div>

      {vehicles.length > 4 && (
        <label className="search lt-search">
          <IconSearch width={17} height={17} />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search name, plate or type"
            aria-label="Search vehicles"
          />
        </label>
      )}

      <div className="lt-scroll">
        {selected && (
          <article className="lt-detail" aria-label={`${selected.name} details`}>
            <div className="lt-detail-top">
              <span
                className="lt-avatar lg"
                style={{ '--veh': vehicleStatusColor(selectedStatus) } as CSSProperties}
                aria-hidden="true"
              >
                <IconTruck width={20} height={20} />
              </span>
              <div className="lt-detail-id">
                <h3>{selected.name}</h3>
                <span>
                  {selected.code} · {vehicleKindLabel(normalizeVehicleKind(selected.kind))}
                </span>
              </div>
              <span className={`lt-chip ${selectedStatus}`}>{vehicleStatusLabel(selectedStatus)}</span>
              <button
                type="button"
                className="icon-btn ghost"
                onClick={() => onSelect(null)}
                aria-label="Close vehicle details"
                title="Close"
              >
                <IconX width={16} height={16} />
              </button>
            </div>

            <dl className="lt-facts">
              <div>
                <dt>
                  <IconGauge width={13} height={13} /> Speed
                </dt>
                <dd>
                  {selectedStatus === 'moving' ? Math.round(selected.speed_kmh) : 0}
                  <small> km/h</small>
                </dd>
              </div>
              <div>
                <dt>
                  <IconClock width={13} height={13} /> Updated
                </dt>
                <dd>{formatLastSeen(selected.last_seen_at, now)}</dd>
              </div>
              <div className="wide">
                <dt>Position</dt>
                <dd className="mono">
                  {hasPosition(selected) ? `${selected.lat.toFixed(5)}, ${selected.lng.toFixed(5)}` : 'No position yet'}
                </dd>
              </div>
            </dl>

            <div className="lt-actions">
              <button
                type="button"
                className={`btn small ${following ? 'primary' : 'secondary'}`}
                aria-pressed={following}
                disabled={!hasPosition(selected)}
                onClick={() => onFollowChange(!following)}
              >
                <IconNavigation width={15} height={15} />
                {following ? 'Following' : 'Follow'}
              </button>
              {!activeRoute && !showForm && (
                <button type="button" className="btn small secondary" onClick={() => setPlanning(true)}>
                  <IconRoute width={15} height={15} />
                  Plan route
                </button>
              )}
            </div>

            {activeRoute && (
              <div className="lt-route">
                <div className="lt-route-ends">
                  <span>
                    <b className="route-pin origin">A</b>
                    {activeRoute.origin.name || 'Origin'}
                  </span>
                  <span className="lt-route-line" aria-hidden="true" />
                  <span>
                    <b className="route-pin dest">B</b>
                    {activeRoute.destination.name || 'Destination'}
                  </span>
                </div>
                <div
                  className="lt-bar"
                  role="progressbar"
                  aria-label="Route progress"
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={Math.round(routeProgress(activeRoute) * 100)}
                >
                  <span style={{ width: `${Math.round(routeProgress(activeRoute) * 100)}%` }} />
                </div>
                <div className="lt-route-stats">
                  <span>
                    <b>{Math.round(routeProgress(activeRoute) * 100)}%</b> done
                  </span>
                  <span>
                    <b>{formatDistance(activeRoute.remaining_m)}</b> left
                  </span>
                  <span>
                    ETA <b>{formatDuration(routeEtaSeconds(activeRoute, selected.speed_kmh))}</b>
                  </span>
                </div>
                <div className="lt-route-foot">
                  <span>
                    {formatDistance(activeRoute.distance_m)} · {formatDuration(activeRoute.duration_s)} ·{' '}
                    {activeRoute.source === 'osrm' ? 'road route' : 'direct line'}
                  </span>
                  <button type="button" className="text-action" onClick={() => void clearRoute(selected.id)}>
                    Clear route
                  </button>
                </div>
              </div>
            )}

            {showForm && (
              <div className="lt-form">
                <p>Tap the target, then tap a point on the map.</p>
                {(['origin', 'destination'] as const).map((stage) => {
                  const stop = draft?.[stage] ?? null
                  const armed = picking === stage
                  const title = stage === 'origin' ? 'Origin' : 'Destination'
                  return (
                    <div className={armed ? 'lt-stop picking' : 'lt-stop'} key={stage}>
                      <span className={`route-pin ${stage === 'origin' ? 'origin' : 'dest'}`} aria-hidden="true">
                        {stage === 'origin' ? 'A' : 'B'}
                      </span>
                      <div className="lt-stop-field">
                        <input
                          value={stop?.name ?? ''}
                          placeholder={stop ? `${title} name (optional)` : `${title} — pick on the map`}
                          aria-label={`${title} label`}
                          disabled={!stop}
                          onChange={(e) => onLabelChange(stage, e.target.value)}
                        />
                        <span>{stop ? `${stop.lat.toFixed(5)}, ${stop.lng.toFixed(5)}` : 'not picked yet'}</span>
                      </div>
                      <button
                        type="button"
                        className={armed ? 'icon-btn active' : 'icon-btn'}
                        aria-pressed={armed}
                        title={armed ? 'Cancel picking' : `Pick ${title.toLowerCase()} on the map`}
                        aria-label={armed ? 'Cancel picking' : `Pick ${title.toLowerCase()} on the map`}
                        onClick={() => (armed ? onPickCancel() : onPickStart(stage))}
                      >
                        <IconCrosshair width={16} height={16} />
                      </button>
                    </div>
                  )
                })}
                <div className="lt-actions">
                  <button
                    type="button"
                    className="btn primary small"
                    disabled={!draft?.origin || !draft?.destination || saving}
                    onClick={() => void submitRoute()}
                  >
                    {saving ? 'Building route…' : 'Build route'}
                  </button>
                  <button
                    type="button"
                    className="btn secondary small"
                    onClick={() => {
                      onClearDraft()
                      setPlanning(false)
                    }}
                  >
                    Cancel
                  </button>
                </div>
              </div>
            )}

            {routeError && (
              <div className="lt-error" role="alert">
                <IconAlert width={15} height={15} />
                <span>{routeError}</span>
              </div>
            )}
          </article>
        )}

        {error && (
          <div className="lt-empty" role="alert">
            <IconAlert width={20} height={20} />
            <p>{error}</p>
            <button type="button" className="btn secondary small" onClick={onRetry}>
              <IconRefresh width={15} height={15} /> Try again
            </button>
          </div>
        )}

        {!error && loading && vehicles.length === 0 && (
          <ul className="lt-list" aria-busy="true" aria-label="Loading the fleet">
            {[0, 1, 2].map((i) => (
              <li key={i} className="lt-skeleton">
                <i />
                <span>
                  <i />
                  <i />
                </span>
              </li>
            ))}
          </ul>
        )}

        {!error && !loading && vehicles.length === 0 && (
          <div className="lt-empty">
            <IconTruck width={22} height={22} />
            <p>No vehicles tracked yet. Start the simulator to feed positions:</p>
            <code className="veh-hint">bin/simulator.exe -api http://localhost:8090 -count 5 -interval 2s</code>
          </div>
        )}

        {vehicles.length > 0 && filtered.length === 0 && (
          <div className="lt-empty">
            <IconSearch width={20} height={20} />
            <p>No vehicles match this filter.</p>
            <button
              type="button"
              className="btn secondary small"
              onClick={() => {
                setFilter('all')
                setQuery('')
              }}
            >
              Reset filter
            </button>
          </div>
        )}

        <ul className="lt-list">
          {filtered.map((v) => {
            const status = normalizeVehicleStatus(v.status)
            const isSel = v.id === selectedId
            const route = routeByVehicle.get(v.id)
            const pct = route ? Math.round(routeProgress(route) * 100) : null
            return (
              <li key={v.id}>
                <button
                  type="button"
                  className={isSel ? 'lt-card selected' : 'lt-card'}
                  aria-pressed={isSel}
                  onClick={() => onSelect(isSel ? null : v.id)}
                  title={`${v.code} · ${v.name}`}
                >
                  <span
                    className={`lt-avatar ${status}`}
                    style={{ '--veh': vehicleStatusColor(status) } as CSSProperties}
                    aria-hidden="true"
                  >
                    <IconTruck width={17} height={17} />
                  </span>
                  <span className="lt-card-body">
                    <span className="lt-card-name">{v.name}</span>
                    <span className="lt-card-sub">
                      {v.code} · {vehicleKindLabel(normalizeVehicleKind(v.kind))}
                    </span>
                    {route && pct !== null ? (
                      <span className="lt-card-route">
                        <span className="lt-bar slim" aria-hidden="true">
                          <span style={{ width: `${pct}%` }} />
                        </span>
                        <em>{pct}%</em>
                      </span>
                    ) : (
                      <span className="lt-card-sub dim">{formatLastSeen(v.last_seen_at, now)}</span>
                    )}
                  </span>
                  <span className="lt-card-end">
                    <span className={`lt-chip ${status}`}>{vehicleStatusLabel(status)}</span>
                    {status === 'moving' && v.speed_kmh > 0 && (
                      <span className="lt-speed">
                        {Math.round(v.speed_kmh)}
                        <small> km/h</small>
                      </span>
                    )}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      </div>
    </section>
  )
}
