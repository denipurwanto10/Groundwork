import type { ReactNode } from 'react'
import type { GeofenceEvent, Overview, Vehicle } from '../lib/types'
import { formatIdleDuration, formatLastSeen, formatOverviewDate, normalizeVehicleStatus } from '../lib/types'
import { Card } from './DashboardParts'
import { IconActivity, IconClock, IconNavigation, IconPolygon, IconTruck } from './Icons'

/* -------------------------------------------------------------------------- */
/* Overview — the aggregate of the day                                          */
/* -------------------------------------------------------------------------- */

/**
 * The numbers the Overview tab opens with.
 *
 * They come from the server (GET /api/overview), not from the browser: the
 * whole history of the reported positions lives in the database, so the
 * kilometres of the day are added up there. The browser only draws them.
 */
export interface OverviewProps {
  data: Overview | null
  loading: boolean
  error: string | null
  onRetry: () => void
}

/** How many crossings the activity list shows. */
const RECENT_LIMIT = 5

/**
 * "210.8" / "4.25" — one decimal once the fleet has driven a real distance,
 * two while the number is still small.
 */
function formatKm(km: number): string {
  if (!Number.isFinite(km) || km < 0) return '—'
  if (km >= 10) return km.toFixed(1)
  return km.toFixed(2)
}

interface StatCard {
  key: string
  label: string
  value: string
  unit?: string
  sub: string
  tone: 'accent' | 'ok' | 'warn' | 'info'
  icon: ReactNode
}

/**
 * The four cards of the day: how far the fleet drove, how many units are
 * moving, how many are standing still or gone, and how often a zone border
 * was crossed.
 */
function statCards(data: Overview): StatCard[] {
  const v = data.vehicles
  const e = data.events
  const standing = v.idle + v.offline

  return [
    {
      key: 'distance',
      label: 'Distance today',
      value: formatKm(data.distance_km),
      unit: 'km',
      sub: 'Whole fleet, every unit',
      tone: 'accent',
      icon: <IconNavigation width={24} height={24} />,
    },
    {
      key: 'moving',
      label: 'Moving now',
      value: String(v.moving),
      unit: `of ${v.total}`,
      sub: v.total > 0 ? `${Math.round((v.moving / v.total) * 100)}% of the fleet on the road` : 'No units yet',
      tone: 'ok',
      icon: <IconTruck width={24} height={24} />,
    },
    {
      key: 'standing',
      label: 'Idle & offline',
      value: String(standing),
      unit: `of ${v.total}`,
      sub: `${v.idle} idle · ${v.offline} offline`,
      tone: 'warn',
      icon: <IconClock width={24} height={24} />,
    },
    {
      key: 'geofence',
      label: 'Zone crossings today',
      value: String(e.total),
      unit: e.total === 1 ? 'event' : 'events',
      sub: `${e.enter} entered · ${e.exit} left a zone`,
      tone: 'info',
      icon: <IconPolygon width={24} height={24} />,
    },
  ]
}

/**
 * The row of summary cards that opens the Overview tab.
 *
 * A card is a big icon, a big number and a label — the three things an
 * operator reads first when they open the page.
 */
export function OverviewStatCards({ data, loading, error, onRetry }: OverviewProps) {
  if (error && !data) {
    return (
      <div className="ov-grid">
        <div className="ov-error">
          <IconActivity width={18} height={18} />
          <span>{error}</span>
          <button type="button" className="d-link" onClick={onRetry}>
            Try again
          </button>
        </div>
      </div>
    )
  }

  if (!data) {
    return (
      <div className="ov-grid">
        {['distance', 'moving', 'standing', 'geofence'].map((k) => (
          <div key={k} className="kpi ov-card ov-skeleton" aria-busy={loading} />
        ))}
      </div>
    )
  }

  return (
    <div className="ov-grid">
      {statCards(data).map((c) => (
        <section key={c.key} className="kpi ov-card">
          <span className={`kpi-icon ${c.tone}`} aria-hidden="true">
            {c.icon}
          </span>
          <span className="kpi-label">{c.label}</span>
          <span className="kpi-value">
            {c.value}
            {c.unit && <small>{c.unit}</small>}
          </span>
          <span className="kpi-sub">{c.sub}</span>
        </section>
      ))}
    </div>
  )
}

/**
 * One small line below the fleet card: the unit that has been standing
 * still the longest.
 *
 * The number comes from the fleet list (GET /api/vehicles), not from the
 * aggregate of the day: the server counts the idle session of every unit and
 * hands the duration along with the fleet. Nothing is shown when no unit is
 * idle.
 */
export function OverviewIdleNote({ vehicles }: { vehicles: Vehicle[] }) {
  const longest = vehicles
    .filter((v) => normalizeVehicleStatus(v.status) === 'idle' && v.idle_duration_s != null)
    .sort((a, b) => (b.idle_duration_s ?? 0) - (a.idle_duration_s ?? 0))[0]

  if (!longest) return null

  return (
    <p className="ov-idle-note">
      <IconClock width={13} height={13} />
      <span>
        idle terlama: <b>{longest.name}</b> · {formatIdleDuration(longest.idle_duration_s)}
      </span>
    </p>
  )
}

/**
 * "Aktivitas Terbaru": what happened today with the zone borders, and the
 * five latest crossings.
 *
 * The two summary lines come first ("3 trucks entered a zone, 2 trucks left
 * a zone"), because that is the question the day is asked for; the crossings
 * underneath say who did it and when.
 */
export function OverviewActivityCard({
  data,
  loading,
  error,
  onRetry,
  onOpenFleet,
}: OverviewProps & { onOpenFleet: () => void }) {
  const events = data?.events
  const recent: GeofenceEvent[] = (data?.recent ?? []).slice(0, RECENT_LIMIT)

  return (
    <Card
      title="Recent activity"
      count={events?.total}
      action={{ label: 'Live view', onClick: onOpenFleet }}
      className="ov-activity"
    >
      {error && !data ? (
        <div className="ov-error">
          <IconActivity width={18} height={18} />
          <span>{error}</span>
          <button type="button" className="d-link" onClick={onRetry}>
            Try again
          </button>
        </div>
      ) : !data ? (
        <p className="d-empty">{loading ? 'Loading the day…' : 'No data yet.'}</p>
      ) : (
        <>
          <div className="ov-summary">
            <div className="ov-summary-row">
              <span className="d-badge ok">IN</span>
              <span>
                <b>{events!.enter}</b> {events!.enter === 1 ? 'truck' : 'trucks'} entered a zone
              </span>
            </div>
            <div className="ov-summary-row">
              <span className="d-badge warn">OUT</span>
              <span>
                <b>{events!.exit}</b> {events!.exit === 1 ? 'truck' : 'trucks'} left a zone
              </span>
            </div>
            <p className="ov-summary-date">Today · {formatOverviewDate(data.date)}</p>
          </div>

          {events!.zones.length > 0 && (
            <ul className="ov-zones">
              {events!.zones.map((z) => (
                <li key={z.zone_id}>
                  <span className="d-row-main">
                    <b>{z.zone_name || `Zone ${z.zone_id}`}</b>
                    <small>
                      {z.enter} in · {z.exit} out
                    </small>
                  </span>
                  <span className="d-row-end">
                    <b>{z.total}</b>
                    <small>crossings</small>
                  </span>
                </li>
              ))}
            </ul>
          )}

          {recent.length === 0 ? (
            <p className="d-empty">
              No crossings yet. They show up here the moment a unit enters or leaves a zone.
            </p>
          ) : (
            <ul className="d-list">
              {recent.map((ev) => (
                <li key={ev.id}>
                  <div className="d-row static">
                    <span className={`d-badge ${ev.event_type === 'ENTER' ? 'ok' : 'warn'}`}>
                      {ev.event_type === 'ENTER' ? 'IN' : 'OUT'}
                    </span>
                    <span className="d-row-main">
                      <b>{ev.vehicle_name}</b>
                      <small>
                        {ev.event_type === 'ENTER' ? 'entered' : 'left'} {ev.zone_name || `zone ${ev.zone_id}`}
                      </small>
                    </span>
                    <span className="d-row-end">
                      <small>{formatLastSeen(ev.created_at)}</small>
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </Card>
  )
}
