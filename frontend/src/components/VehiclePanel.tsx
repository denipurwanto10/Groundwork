import { useEffect, useMemo, useState } from 'react'
import type { CSSProperties } from 'react'
import type { StreamState } from '../lib/vehicleStream'
import type {
  GeofenceEvent,
  RouteStop,
  UserAccount,
  Vehicle,
  VehicleInput,
  VehicleKind,
  VehicleRoute,
  VehicleStatus,
} from '../lib/types'
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
  VEHICLE_KIND_LABELS,
  VEHICLE_KINDS,
} from '../lib/types'
import { ApiError } from '../lib/api'
import {
  IconAlert,
  IconClock,
  IconCrosshair,
  IconEdit,
  IconEye,
  IconEyeOff,
  IconFit,
  IconGauge,
  IconMotorcycle,
  IconNavigation,
  IconPickup,
  IconPlus,
  IconRefresh,
  IconRoute,
  IconSearch,
  IconTrash,
  IconTruck,
  IconVan,
  IconX,
} from './Icons'
import Select from './Select'
import FormModal from './FormModal'
import { VehicleSide } from './VehicleArt'

const STREAM_UI: Record<StreamState, { label: string; title: string }> = {
  connecting: { label: 'Connecting', title: 'Opening the live position feed' },
  live: { label: 'Live', title: 'Receiving positions as they are reported' },
  reconnecting: { label: 'Reconnecting', title: 'The feed dropped; reconnecting on its own' },
}

type Filter = 'all' | VehicleStatus

/**
 * Badge glyph for one unit, drawn with the same shapes as its map marker so the
 * list never shows a truck for a van or a motorcycle.
 */
function KindGlyph({ kind, size }: { kind: string; size: number }) {
  switch (normalizeVehicleKind(kind)) {
    case 'van':
      return <IconVan width={size} height={size} />
    case 'motorcycle':
      return <IconMotorcycle width={size} height={size} />
    case 'pickup':
      return <IconPickup width={size} height={size} />
    default:
      return <IconTruck width={size} height={size} />
  }
}

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
  /** The plan starts at the unit's own position; the operator picks the target. */
  onPlanStart: (vehicleId: number) => void
  /** A plan is on its way to the server. */
  routeBusy: boolean
  onClearDraft: () => void
  onRouteSet: (vehicleId: number, origin: RouteStop, destination: RouteStop) => Promise<void>
  onRouteClear: (vehicleId: number) => Promise<void>
  /** Only admins may plan or clear routes; viewer and driver stay read-only. */
  canEdit: boolean
  /** Whether this account may move a unit by dragging its marker on the map. */
  canDrive: (v: Vehicle) => boolean
  /** Admins add, edit and remove units; the other roles stay read-only. */
  onCreate: (input: VehicleInput) => Promise<void>
  onUpdate: (id: number, input: VehicleInput) => Promise<void>
  onDelete: (vehicle: Vehicle) => void
  /** Driver accounts a unit can be handed over to. */
  drivers: UserAccount[]
  /** Zone boundary crossings, newest first. */
  events: GeofenceEvent[]
}

/** The unit the fleet form is adding or editing. */
interface UnitDraft {
  id: number | null
  name: string
  code: string
  kind: VehicleKind
  driverId: number | ''
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
  onPlanStart,
  routeBusy,
  onClearDraft,
  onRouteSet,
  onRouteClear,
  canEdit,
  canDrive,
  onCreate,
  onUpdate,
  onDelete,
  drivers,
  events,
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
  const [unit, setUnit] = useState<UnitDraft | null>(null)
  const [unitError, setUnitError] = useState<string | null>(null)
  const [unitFields, setUnitFields] = useState<Record<string, string>>({})
  const [unitSaving, setUnitSaving] = useState(false)

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
    setPlanning(false)
    try {
      await onRouteClear(vehicleId)
    } catch (err) {
      setRouteError(err instanceof Error ? err.message : 'Could not clear the route')
    }
  }

  const openCreate = () => {
    setUnit({ id: null, name: '', code: '', kind: 'truck', driverId: '' })
    setUnitError(null)
    setUnitFields({})
  }

  const openEdit = (v: Vehicle) => {
    setUnit({
      id: v.id,
      name: v.name,
      code: v.code,
      kind: normalizeVehicleKind(v.kind),
      driverId: v.driver_id ?? '',
    })
    setUnitError(null)
    setUnitFields({})
  }

  const closeUnit = () => {
    setUnit(null)
    setUnitError(null)
    setUnitFields({})
  }

  const submitUnit = async () => {
    if (!unit) return
    const input: VehicleInput = {
      name: unit.name,
      code: unit.code,
      kind: unit.kind,
      driver_id: unit.driverId === '' ? null : unit.driverId,
    }
    setUnitSaving(true)
    setUnitError(null)
    setUnitFields({})
    try {
      if (unit.id === null) await onCreate(input)
      else await onUpdate(unit.id, input)
      closeUnit()
    } catch (err) {
      if (err instanceof ApiError) {
        setUnitError(err.message)
        setUnitFields(err.fields ?? {})
      } else {
        setUnitError(err instanceof Error ? err.message : 'Could not save the vehicle')
      }
    } finally {
      setUnitSaving(false)
    }
  }

  const unitFieldError = (field: string) =>
    unitFields[field] ? (
      <span className="um-error" role="alert">
        {unitFields[field]}
      </span>
    ) : null

  const selectedStatus = selected ? normalizeVehicleStatus(selected.status) : 'offline'
  const showForm = !!selected && !activeRoute && (planning || !!draft?.origin || !!draft?.destination)
  // The plan is on its way to the server: the form waits for the line.
  const busy = saving || routeBusy

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

      {canEdit && (
        <div className="lt-add-row">
          <button type="button" className="btn primary small" onClick={openCreate}>
            <IconPlus width={15} height={15} />
            Add vehicle
          </button>
        </div>
      )}

      <div className="lt-stats" role="group" aria-label="Filter by status">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            className={`lt-stat ${f.key}${filter === f.key ? ' on' : ''}`}
            aria-pressed={filter === f.key}
            onClick={() => setFilter(f.key)}
          >
            <span className="lt-stat-label">{f.label}</span>
            <b>{counts[f.key]}</b>
            <i
              className="lt-stat-bar"
              aria-hidden="true"
              style={{ '--share': counts.all ? `${Math.round((counts[f.key] / counts.all) * 100)}%` : '0%' } as CSSProperties}
            />
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
          <article className={`lt-detail ${selectedStatus}`} aria-label={`${selected.name} details`}>
            <div className="lt-detail-top">
              <span
                className="lt-avatar lg"
                style={{ '--veh': vehicleStatusColor(selectedStatus) } as CSSProperties}
                aria-hidden="true"
              >
                <VehicleSide kind={selected.kind} />
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

            {canDrive(selected) && hasPosition(selected) && (
              <p className="lt-drive-hint">Drag this unit's marker on the map to drive it.</p>
            )}

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
              {canEdit && !activeRoute && !showForm && (
                <button
                  type="button"
                  className="btn small secondary"
                  onClick={() => {
                    if (!selected) return
                    // The plan starts where the unit stands; the operator only
                    // has to pick the target on the map.
                    onPlanStart(selected.id)
                    setPlanning(true)
                  }}
                >
                  <IconRoute width={15} height={15} />
                  Plan route
                </button>
              )}
            </div>

            {activeRoute && (
              <div className="lt-route">
                <div className="lt-route-heading">
                  <IconRoute width={14} height={14} />
                  <span>
                    Following route to <b>{activeRoute.destination.name || 'the destination'}</b>
                  </span>
                </div>
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
                  {canEdit && (
                    <button type="button" className="text-action" onClick={() => void clearRoute(selected.id)}>
                      Clear route
                    </button>
                  )}
                </div>
              </div>
            )}

            {showForm && canEdit && (
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
                    disabled={!draft?.origin || !draft?.destination || busy}
                    onClick={() => void submitRoute()}
                  >
                    {busy ? 'Building route…' : 'Build route'}
                  </button>
                  <button
                    type="button"
                    className="btn secondary small"
                    disabled={busy}
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
              <li key={v.id} className={isSel ? 'lt-row selected' : 'lt-row'}>
                <button
                  type="button"
                  className="lt-card"
                  aria-pressed={isSel}
                  onClick={() => onSelect(isSel ? null : v.id)}
                  title={`${v.code} · ${v.name}`}
                >
                  <span
                    className={`lt-avatar ${status}`}
                    style={{ '--veh': vehicleStatusColor(status) } as CSSProperties}
                    aria-hidden="true"
                  >
                    <VehicleSide kind={v.kind} />
                  </span>
                  <span className="lt-card-body">
                    <span className="lt-card-name">{v.name}</span>
                    <span className="lt-card-sub">
                      {v.code} · {vehicleKindLabel(normalizeVehicleKind(v.kind))}
                    </span>
                    {route && pct !== null ? (
                      <span className="lt-card-route">
                        <span className="lt-card-route-name">
                          <IconRoute width={12} height={12} />
                          Following route to <b>{route.destination.name || 'the destination'}</b>
                        </span>
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
                {canEdit && (
                  <span className="lt-row-actions">
                    <button
                      type="button"
                      className="icon-btn"
                      onClick={() => openEdit(v)}
                      aria-label={`Edit ${v.name}`}
                      title="Edit vehicle"
                    >
                      <IconEdit width={15} height={15} />
                    </button>
                    <button
                      type="button"
                      className="icon-btn"
                      onClick={() => onDelete(v)}
                      aria-label={`Delete ${v.name}`}
                      title="Delete vehicle"
                    >
                      <IconTrash width={15} height={15} />
                    </button>
                  </span>
                )}
              </li>
            )
          })}
        </ul>
      </div>

      {events.length > 0 && (
        <div className="lt-events">
          <div className="lt-events-head">
            <span>Zone crossings</span>
            <span>{events.length}</span>
          </div>
          <ul className="lt-events-list">
            {events.map((ev) => {
              const entering = ev.event_type === 'ENTER'
              return (
                <li key={ev.id} className={`lt-event ${entering ? 'enter' : 'exit'}`}>
                  <span className="lt-event-icon">{entering ? <IconNavigation width={14} height={14} /> : <IconAlert width={14} height={14} />}</span>
                  <span className="lt-event-body">
                    <strong>{ev.vehicle_name}</strong>
                    <span className="lt-event-text">
                      {entering ? 'MASUK ke' : 'KELUAR dari'} {ev.zone_name}
                    </span>
                  </span>
                  <span className="lt-event-time">{formatLastSeen(ev.created_at, now)}</span>
                </li>
              )
            })}
          </ul>
        </div>
      )}

      {unit && (
        <FormModal
          titleId="veh-form-title"
          title={unit.id === null ? 'Add vehicle' : 'Edit vehicle'}
          subtitle={
            unit.id === null
              ? 'Register a unit so it shows up on the live map.'
              : `Update the details of ${unit.name.trim() || 'this vehicle'}.`
          }
          icon={<KindGlyph kind={unit.kind} size={22} />}
          onClose={closeUnit}
          footer={
            <>
              <button type="button" className="btn secondary" onClick={closeUnit} disabled={unitSaving}>
                Cancel
              </button>
              <button type="button" className="btn primary" onClick={() => void submitUnit()} disabled={unitSaving}>
                {unitSaving ? 'Saving…' : unit.id === null ? 'Add vehicle' : 'Save changes'}
              </button>
            </>
          }
        >
          <div className="um-fgrid">
            <div className="um-field">
              <label htmlFor="veh-name">Name</label>
              <input
                id="veh-name"
                value={unit.name}
                maxLength={120}
                autoComplete="off"
                autoFocus
                onChange={(e) => setUnit({ ...unit, name: e.target.value })}
                placeholder="Truck 01"
                aria-invalid={!!unitFields.name}
              />
              {unitFieldError('name')}
            </div>

            <div className="um-field">
              <label htmlFor="veh-code">Plate number</label>
              <input
                id="veh-code"
                value={unit.code}
                maxLength={32}
                autoComplete="off"
                onChange={(e) => setUnit({ ...unit, code: e.target.value })}
                placeholder="B 1234 XYZ"
                aria-invalid={!!unitFields.code}
              />
              {unitFieldError('code')}
            </div>

            <div className="um-field">
              <label htmlFor="veh-kind">Type</label>
              <Select
                id="veh-kind"
                label="Type"
                value={unit.kind}
                options={VEHICLE_KINDS.map((k) => ({ value: k, label: VEHICLE_KIND_LABELS[k] }))}
                onChange={(v) => setUnit({ ...unit, kind: v as VehicleKind })}
                invalid={!!unitFields.kind}
              />
              {unitFieldError('kind')}
            </div>

            <div className="um-field">
              <label htmlFor="veh-driver">Driver</label>
              <Select
                id="veh-driver"
                label="Driver"
                value={String(unit.driverId)}
                options={[
                  { value: '', label: 'Unassigned' },
                  ...drivers.map((d) => ({
                    value: String(d.id),
                    // Name is optional, so fall back to the login handle.
                    label: d.name?.trim() ? `${d.name.trim()} · ${d.username}` : d.username || d.email || `#${d.id}`,
                  })),
                ]}
                onChange={(v) => setUnit({ ...unit, driverId: v === '' ? '' : Number(v) })}
              />
              {unitFieldError('driver_id')}
            </div>
          </div>

          {unitError && (
            <div className="um-alert" role="alert">
              <IconAlert width={16} height={16} />
              <span>{unitError}</span>
            </div>
          )}
        </FormModal>
      )}
    </section>
  )
}
