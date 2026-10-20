import { REGION_LEVELS } from '../lib/types'
import { IconLayers, IconChevronRight, IconChevronUp, IconRefresh } from './Icons'

export interface RegionPathEntry {
  code: string
  name: string
}

interface Props {
  /** Administrative level currently shown (1 = province, 2 = regency/city). */
  level: number
  /** Breadcrumb of the drill-down; the last entry is the current parent. */
  path: RegionPathEntry[]
  /** Number of boundaries loaded for the current level. */
  count: number
  /** True while the boundaries are being fetched. */
  loading: boolean
  /** Boundaries are drawn on the map. */
  visible: boolean
  /** Shown when the current level has no data in the database yet. */
  note: string | null
  onVisibleChange: (visible: boolean) => void
  /**
   * Jump to a breadcrumb entry: -1 is the country root, otherwise the index
   * into `path`. App owns the level/parent bookkeeping.
   */
  onJumpTo: (crumbIndex: number) => void
  /** Re-fetch the current level. */
  onRefresh: () => void
}

/**
 * Floating control for the official administrative boundaries: shows the
 * current drill-down path and lets the operator go back up or reload.
 */
export default function RegionControls({
  level,
  path,
  count,
  loading,
  visible,
  note,
  onVisibleChange,
  onJumpTo,
  onRefresh,
}: Props) {
  const levelInfo = REGION_LEVELS.find((l) => l.level === level)
  const levelLabel = levelInfo?.plural.toLowerCase() ?? `level ${level}`
  const lastIndex = path.length - 1

  const crumbs: { label: string; index: number }[] = [
    { label: 'Indonesia', index: -1 },
    ...path.map((p, i) => ({ label: p.name, index: i })),
  ]

  return (
    <div className="region-controls" role="group" aria-label="Administrative boundaries">
      <div className="region-controls-row">
        <button
          type="button"
          className={`icon-btn${visible ? ' active' : ''}`}
          onClick={() => onVisibleChange(!visible)}
          aria-pressed={visible}
          title={visible ? 'Hide administrative boundaries' : 'Show administrative boundaries'}
        >
          <IconLayers width={16} height={16} />
        </button>
        <span className="region-controls-label">
          {visible ? (loading ? 'Loading…' : levelLabel) : 'Boundaries hidden'}
        </span>
        <button
          type="button"
          className="icon-btn"
          onClick={onRefresh}
          disabled={loading}
          title="Reload boundaries"
          aria-label="Reload boundaries"
        >
          <IconRefresh width={15} height={15} />
        </button>
        {path.length > 0 && (
          <button
            type="button"
            className="icon-btn"
            onClick={() => onJumpTo(lastIndex - 1)}
            title="Go up one level"
            aria-label="Go up one level"
          >
            <IconChevronUp width={15} height={15} />
          </button>
        )}
      </div>

      {visible && (
        <nav className="region-breadcrumb" aria-label="Boundary drill-down">
          {crumbs.map((c, i) => (
            <span key={`${c.index}-${c.label}`} className="region-crumb">
              {i > 0 && <IconChevronRight width={12} height={12} />}
              <button
                type="button"
                className={i === crumbs.length - 1 ? 'current' : undefined}
                aria-current={i === crumbs.length - 1 ? 'true' : undefined}
                onClick={() => onJumpTo(c.index)}
              >
                {c.label}
              </button>
            </span>
          ))}
        </nav>
      )}

      {visible && note && <p className="region-controls-note">{note}</p>}

      {visible && !loading && !note && (
        <p className="region-controls-note">
          {count} {levelLabel}
        </p>
      )}
    </div>
  )
}
