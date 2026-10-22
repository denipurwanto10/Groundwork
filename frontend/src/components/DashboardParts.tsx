import type { CSSProperties, ReactNode } from 'react'
import type { Entity, Vehicle, ZoneFeature } from '../lib/types'
import {
  ZONE_CATEGORY_LABELS,
  formatLastSeen,
  hasPosition,
  normalizeVehicleStatus,
  typeColorFor,
  vehicleKindLabel,
  vehicleStatusColor,
  vehicleStatusLabel,
  canonicalType,
} from '../lib/types'
import { IconArrowRight } from './Icons'
import { VehicleSide } from './VehicleArt'

/* -------------------------------------------------------------------------- */
/* Shared card chrome                                                          */
/* -------------------------------------------------------------------------- */

export function Card({
  title,
  count,
  action,
  children,
  className = '',
}: {
  title: string
  /** A number next to the title: how many rows the card holds. */
  count?: number
  action?: { label: string; onClick: () => void }
  children: ReactNode
  className?: string
}) {
  return (
    <section className={`d-card ${className}`}>
      <header className="d-card-head">
        <h2>
          {title}
          {count != null && count > 0 && <span className="d-count">{count}</span>}
        </h2>
        {action && (
          <button type="button" className="d-link" onClick={action.onClick}>
            {action.label}
            <IconArrowRight width={14} height={14} />
          </button>
        )}
      </header>
      {children}
    </section>
  )
}

function Empty({ children }: { children: ReactNode }) {
  return <p className="d-empty">{children}</p>
}

/* -------------------------------------------------------------------------- */
/* Fleet                                                                       */
/* -------------------------------------------------------------------------- */

const STATUS_ORDER: Record<string, number> = { moving: 0, idle: 1, offline: 2 }

export function FleetCard({
  vehicles,
  counts,
  onSelect,
  onOpen,
}: {
  vehicles: Vehicle[]
  counts: { moving: number; idle: number; offline: number }
  onSelect: (id: number) => void
  onOpen: () => void
}) {
  const total = vehicles.length
  const rows = [...vehicles]
    .sort((a, b) => (STATUS_ORDER[a.status] ?? 3) - (STATUS_ORDER[b.status] ?? 3) || b.speed_kmh - a.speed_kmh)
    .slice(0, 6)
  const seg = (n: number) => (total ? `${(n / total) * 100}%` : '0%')

  return (
    <Card title="Fleet status" action={{ label: 'Open fleet', onClick: onOpen }}>
      {total === 0 ? (
        <Empty>No vehicles yet. Add a unit from the Fleet page.</Empty>
      ) : (
        <>
          <div className="fleet-summary">
            {(['moving', 'idle', 'offline'] as const).map((st) => (
              <div key={st} className={`fleet-tile ${st}`} style={{ '--c': vehicleStatusColor(st) } as CSSProperties}>
                <b>{counts[st]}</b>
                <span>{vehicleStatusLabel(st)}</span>
              </div>
            ))}
          </div>
          <div
            className="fleet-bar"
            role="img"
            aria-label={`${counts.moving} moving, ${counts.idle} idle, ${counts.offline} offline`}
          >
            <i style={{ width: seg(counts.moving), background: vehicleStatusColor('moving') }} />
            <i style={{ width: seg(counts.idle), background: vehicleStatusColor('idle') }} />
            <i style={{ width: seg(counts.offline), background: vehicleStatusColor('offline') }} />
          </div>
          <ul className="d-list fleet-list">
            {rows.map((v) => {
              const st = normalizeVehicleStatus(v.status)
              return (
                <li key={v.id}>
                  <button type="button" className="d-row fleet-row" onClick={() => onSelect(v.id)} disabled={!hasPosition(v)}>
                    <span className={`fleet-art ${st}`} style={{ '--veh': vehicleStatusColor(st) } as CSSProperties}>
                      <VehicleSide kind={v.kind} />
                    </span>
                    <span className="d-row-main">
                      <b>{v.name}</b>
                      <small>
                        <em className="fleet-plate">{v.code}</em> {vehicleKindLabel(v.kind)}
                      </small>
                    </span>
                    <span className="d-row-end">
                      {st === 'moving' ? (
                        <b className="fleet-speed">
                          {Math.round(v.speed_kmh)}
                          <small> km/h</small>
                        </b>
                      ) : (
                        <span className={`lt-chip ${st}`}>{vehicleStatusLabel(st)}</span>
                      )}
                      <small>{formatLastSeen(v.last_seen_at)}</small>
                    </span>
                  </button>
                </li>
              )
            })}
          </ul>
        </>
      )}
    </Card>
  )
}

/* -------------------------------------------------------------------------- */
/* Places by type                                                              */
/* -------------------------------------------------------------------------- */

export function TypeBreakdownCard({ entities, onOpen }: { entities: Entity[]; onOpen: () => void }) {
  const byType = new Map<string, number>()
  for (const e of entities) {
    const t = canonicalType(e.type) || 'other'
    byType.set(t, (byType.get(t) ?? 0) + 1)
  }
  const rows = [...byType.entries()].sort((a, b) => b[1] - a[1]).slice(0, 8)
  const max = Math.max(1, ...rows.map((r) => r[1]))

  return (
    <Card title="Places by type" count={entities.length} action={{ label: 'All places', onClick: onOpen }}>
      {rows.length === 0 ? (
        <Empty>No places yet.</Empty>
      ) : (
        <ul className="bars">
          {rows.map(([type, n]) => (
            <li key={type}>
              <span className="bar-label">{type.charAt(0).toUpperCase() + type.slice(1)}</span>
              <span className="bar-track">
                <i style={{ width: `${(n / max) * 100}%`, background: typeColorFor(type) }} />
              </span>
              <b>{n}</b>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

/* -------------------------------------------------------------------------- */
/* Zones                                                                       */
/* -------------------------------------------------------------------------- */

export function ZonesCard({
  zones,
  onSelect,
  onOpen,
}: {
  zones: ZoneFeature[]
  onSelect: (id: number) => void
  onOpen: () => void
}) {
  return (
    <Card title="Zones" count={zones.length} action={{ label: 'Manage zones', onClick: onOpen }}>
      {zones.length === 0 ? (
        <Empty>No zones drawn yet.</Empty>
      ) : (
        <ul className="d-list">
          {zones.slice(0, 6).map((z) => (
            <li key={z.properties.id}>
              <button type="button" className="d-row" onClick={() => onSelect(z.properties.id)}>
                <span className="d-swatch" style={{ background: z.properties.color }} />
                <span className="d-row-main">
                  <b>{z.properties.name}</b>
                  <small>
                    {ZONE_CATEGORY_LABELS[z.properties.category] ?? z.properties.category} ·{' '}
                    {z.properties.area_km2.toLocaleString(undefined, { maximumFractionDigits: 1 })} km²
                  </small>
                </span>
                <span className="d-row-end">
                  <b>{z.properties.entity_count ?? 0}</b>
                  <small>places</small>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}
