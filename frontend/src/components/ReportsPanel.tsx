import { useMemo } from 'react'
import type { Report, ReportVehicle, ReportZone, Vehicle } from '../lib/types'
import { formatReportDate } from '../lib/types'
import DatePicker from './DatePicker'
import Select from './Select'
import { IconAlert, IconClipboard, IconDownload, IconNavigation, IconPolygon, IconRoute, IconTruck } from './Icons'

/** The periods the panel offers. */
export type ReportPreset = 'today' | 'week' | 'month' | 'custom'

/** The presets and the words they carry. */
const PRESETS: { key: ReportPreset; label: string }[] = [
  { key: 'today', label: 'Today' },
  { key: 'week', label: '7 days' },
  { key: 'month', label: '30 days' },
  { key: 'custom', label: 'Custom' },
]

interface Props {
  /** The report of the period; null while it is not loaded. */
  data: Report | null
  loading: boolean
  error: string | null
  /** The units of the fleet, for the filter. */
  vehicles: Vehicle[]
  /** The unit the report is narrowed to ('' = every unit). */
  vehicleId: string
  /** The first day of the period (YYYY-MM-DD). */
  from: string
  /** The last day of the period (YYYY-MM-DD). */
  to: string
  /** Which preset the period follows. */
  preset: ReportPreset
  /** A driver only ever sees their own unit, so the filter is hidden. */
  scoped: boolean
  /** True while the CSV is being fetched. */
  exporting: boolean
  onPresetChange(preset: ReportPreset): void
  onFromChange(day: string): void
  onToChange(day: string): void
  onVehicleChange(id: string): void
  onRetry(): void
  onExport(): void
}

/** Today, in the format the date pickers speak. */
function todayISO(): string {
  const n = new Date()
  const pad = (v: number) => String(v).padStart(2, '0')
  return `${n.getFullYear()}-${pad(n.getMonth() + 1)}-${pad(n.getDate())}`
}

/** "1,234.56" — the kilometres of the report always show two decimals. */
function formatKm(km: number): string {
  if (!Number.isFinite(km)) return '0.00'
  return km.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/**
 * The operational report: what the fleet did in a period of time.
 *
 * The numbers are counted by the server (GET /api/reports), not by the
 * browser: the whole history of the reported positions lives in the
 * database, and shipping it to the client only to add it up would be both
 * slow and pointless. The panel only draws what it is given.
 */
export default function ReportsPanel({
  data,
  loading,
  error,
  vehicles,
  vehicleId,
  from,
  to,
  preset,
  scoped,
  exporting,
  onPresetChange,
  onFromChange,
  onToChange,
  onVehicleChange,
  onRetry,
  onExport,
}: Props) {
  const vehicleOptions = useMemo(
    () => [
      { value: '', label: 'All vehicles' },
      ...vehicles.map((v) => ({ value: String(v.id), label: `${v.name} · ${v.code}` })),
    ],
    [vehicles],
  )

  const totals = data?.totals
  const empty = !loading && !error && data !== null && data.by_vehicle.length === 0 && data.by_zone.length === 0

  return (
    <section className="rp" aria-label="Operational report">
      <div className="rp-toolbar">
        <div className="rp-presets" role="group" aria-label="Period of the report">
          {PRESETS.map((p) => (
            <button
              key={p.key}
              type="button"
              className={`rp-preset${preset === p.key ? ' on' : ''}`}
              aria-pressed={preset === p.key}
              onClick={() => onPresetChange(p.key)}
            >
              {p.label}
            </button>
          ))}
        </div>

        <div className="rp-dates">
          <DatePicker value={from} max={to} onChange={onFromChange} ariaLabel="First day of the period" />
          <span className="rp-dash" aria-hidden="true">
            –
          </span>
          <DatePicker value={to} max={todayISO()} onChange={onToChange} ariaLabel="Last day of the period" />
        </div>

        {!scoped && (
          <Select
            id="rp-vehicle"
            label="Vehicle"
            value={vehicleId}
            options={vehicleOptions}
            onChange={(v) => onVehicleChange(v)}
            className="rp-filter"
          />
        )}

        <button
          type="button"
          className="btn primary small rp-export"
          onClick={onExport}
          disabled={loading || exporting}
          title="Download the report as a CSV file"
        >
          <IconDownload width={14} height={14} />
          {exporting ? 'Exporting…' : 'Export CSV'}
        </button>
      </div>

      {error && (
        <div className="rp-error" role="alert">
          <IconAlert width={18} height={18} />
          <span>{error}</span>
          <button type="button" className="btn secondary small" onClick={onRetry}>
            Try again
          </button>
        </div>
      )}

      {!error && !totals && (
        <div className="ov-grid rp-cards">
          {['distance', 'routes', 'tasks', 'enter', 'exit'].map((k) => (
            <div key={k} className="kpi ov-card ov-skeleton" aria-busy={loading} />
          ))}
        </div>
      )}

      {totals && (
        <>
          <div className="ov-grid rp-cards">
            <section className="kpi ov-card">
              <span className="kpi-icon accent" aria-hidden="true">
                <IconNavigation width={24} height={24} />
              </span>
              <span className="kpi-label">Distance driven</span>
              <span className="kpi-value">
                {formatKm(totals.distance_km)}
                <small>km</small>
              </span>
              <span className="kpi-sub">{data?.period ? `${formatReportDate(data.period.from)} – ${formatReportDate(data.period.to)}` : ''}</span>
            </section>

            <section className="kpi ov-card">
              <span className="kpi-icon ok" aria-hidden="true">
                <IconRoute width={24} height={24} />
              </span>
              <span className="kpi-label">Routes finished</span>
              <span className="kpi-value">
                {totals.routes_completed}
                <small>of {totals.routes_total}</small>
              </span>
              <span className="kpi-sub">Deliveries closed in the period</span>
            </section>

            <section className="kpi ov-card">
              <span className="kpi-icon warn" aria-hidden="true">
                <IconClipboard width={24} height={24} />
              </span>
              <span className="kpi-label">Work orders done</span>
              <span className="kpi-value">
                {totals.tasks_completed}
                <small>of {totals.tasks_total}</small>
              </span>
              <span className="kpi-sub">Tasks created in the period</span>
            </section>

            <section className="kpi ov-card">
              <span className="kpi-icon info" aria-hidden="true">
                <IconPolygon width={24} height={24} />
              </span>
              <span className="kpi-label">Zones entered</span>
              <span className="kpi-value">
                {totals.geofence_enter}
                <small>crossings</small>
              </span>
              <span className="kpi-sub">Border crossings into a work area</span>
            </section>

            <section className="kpi ov-card">
              <span className="kpi-icon accent" aria-hidden="true">
                <IconTruck width={24} height={24} />
              </span>
              <span className="kpi-label">Zones left</span>
              <span className="kpi-value">
                {totals.geofence_exit}
                <small>crossings</small>
              </span>
              <span className="kpi-sub">Border crossings out of a work area</span>
            </section>
          </div>

          {empty && (
            <div className="rp-empty">
              <IconRoute width={20} height={20} />
              <span>
                Nothing happened in this period. Pick another one, or let the fleet drive a little.
              </span>
            </div>
          )}

          {!empty && (
            <>
              <div className="rp-block">
                <h3 className="rp-title">Per vehicle</h3>
                <div className="rp-table-wrap">
                  <table className="rp-table">
                    <thead>
                      <tr>
                        <th scope="col">Vehicle</th>
                        <th scope="col">Driver</th>
                        <th scope="col" className="num">
                          Distance
                        </th>
                        <th scope="col" className="num">
                          Routes
                        </th>
                        <th scope="col" className="num">
                          Zones in
                        </th>
                        <th scope="col" className="num">
                          Zones out
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {data!.by_vehicle.map((v) => (
                        <VehicleRow key={v.vehicle_id} row={v} />
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>

              <div className="rp-block">
                <h3 className="rp-title">Per zone</h3>
                {data!.by_zone.length === 0 ? (
                  <p className="rp-note">No zone was crossed in this period.</p>
                ) : (
                  <div className="rp-table-wrap">
                    <table className="rp-table">
                      <thead>
                        <tr>
                          <th scope="col">Zone</th>
                          <th scope="col" className="num">
                            Entered
                          </th>
                          <th scope="col" className="num">
                            Left
                          </th>
                          <th scope="col" className="num">
                            Crossings
                          </th>
                          <th scope="col" className="num">
                            Vehicles
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {data!.by_zone.map((z) => (
                          <ZoneRow key={z.zone_id} row={z} />
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </>
          )}
        </>
      )}
    </section>
  )
}

/** One unit in the table of the report. */
function VehicleRow({ row }: { row: ReportVehicle }) {
  return (
    <tr>
      <th scope="row">
        <b>{row.vehicle_name}</b>
        <small>{row.vehicle_code}</small>
      </th>
      <td>{row.driver_name || '—'}</td>
      <td className="num">{formatKm(row.distance_km)} km</td>
      <td className="num">
        {row.routes_completed}
        <small> of {row.routes_total}</small>
      </td>
      <td className="num">{row.geofence_enter}</td>
      <td className="num">{row.geofence_exit}</td>
    </tr>
  )
}

/** One work area in the table of the report. */
function ZoneRow({ row }: { row: ReportZone }) {
  return (
    <tr>
      <th scope="row">{row.zone_name || `Zone ${row.zone_id}`}</th>
      <td className="num">{row.enter}</td>
      <td className="num">{row.exit}</td>
      <td className="num">{row.total}</td>
      <td className="num">{row.unique_vehicles}</td>
    </tr>
  )
}
