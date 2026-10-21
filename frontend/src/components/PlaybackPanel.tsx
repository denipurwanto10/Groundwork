import { useEffect, useRef } from 'react'
import type { HistoryPoint } from '../lib/types'
import { PLAYBACK_SPEEDS } from '../lib/types'
import { IconAlert, IconClock, IconPlay, IconPause, IconRefresh, IconX } from './Icons'

interface Props {
  /** The unit the track belongs to. */
  vehicleName: string
  /** The day the history is read from, YYYY-MM-DD. */
  date: string
  /** The recorded positions, oldest first. */
  points: HistoryPoint[]
  /** Index of the point the playback stands at. */
  index: number
  /** True while the track is running. */
  playing: boolean
  /** How fast the track runs. */
  speed: number
  loading: boolean
  error: string | null
  onDateChange(date: string): void
  onLoad(): void
  onToggle(): void
  onSeek(index: number): void
  onSpeedChange(speed: number): void
  onClose(): void
}

/** The time of a recorded position, in the time zone of the browser. */
function formatTime(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '--:--'
  return new Date(t).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

/**
 * The playback panel: pick a day, load the track, then walk along it.
 *
 * It sits inside the vehicle card, so the map stays visible while the track
 * is running.
 */
export default function PlaybackPanel({
  vehicleName,
  date,
  points,
  index,
  playing,
  speed,
  loading,
  error,
  onDateChange,
  onLoad,
  onToggle,
  onSeek,
  onSpeedChange,
  onClose,
}: Props) {
  const current = points[index]
  const last = points.length - 1
  const panelRef = useRef<HTMLDivElement | null>(null)

  // The panel opens under the button that was pressed, which can be out of
  // sight in the scrolling fleet list: bring it into view once.
  useEffect(() => {
    panelRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [])

  return (
    <div className="pb" ref={panelRef}>
      <div className="pb-head">
        <span className="pb-title">
          <IconClock width={14} height={14} />
          Playback · {vehicleName}
        </span>
        <button type="button" className="icon-btn ghost" onClick={onClose} aria-label="Close playback" title="Close">
          <IconX width={15} height={15} />
        </button>
      </div>

      <div className="pb-load">
        <label className="pb-date">
          <span className="sr-only">Day</span>
          <input
            type="date"
            value={date}
            max={date}
            onChange={(e) => onDateChange(e.target.value)}
            aria-label="Day of the history"
          />
        </label>
        <button type="button" className="btn secondary small" onClick={onLoad} disabled={loading}>
          <IconRefresh width={14} height={14} />
          {loading ? 'Loading…' : 'Load history'}
        </button>
      </div>

      {error && (
        <p className="pb-error" role="alert">
          <IconAlert width={14} height={14} />
          {error}
        </p>
      )}

      {points.length > 0 && (
        <>
          <div className="pb-controls">
            <button
              type="button"
              className="btn primary small pb-play"
              onClick={onToggle}
              aria-label={playing ? 'Pause the playback' : 'Play the track'}
            >
              {playing ? <IconPause width={14} height={14} /> : <IconPlay width={14} height={14} />}
              {playing ? 'Pause' : 'Play'}
            </button>

            <label className="pb-speed">
              <span className="sr-only">Speed</span>
              <select
                value={String(speed)}
                onChange={(e) => onSpeedChange(Number(e.target.value))}
                aria-label="Playback speed"
              >
                {PLAYBACK_SPEEDS.map((s) => (
                  <option key={s} value={s}>
                    {s}x
                  </option>
                ))}
              </select>
            </label>

            <span className="pb-readout">
              <b>{current ? formatTime(current.timestamp) : '--:--:--'}</b>
              <em>
                {current ? Math.round(current.speed) : 0} km/h
              </em>
            </span>
          </div>

          <input
            className="pb-slider"
            type="range"
            min={0}
            max={last}
            step={1}
            value={Math.min(index, last)}
            onChange={(e) => onSeek(Number(e.target.value))}
            aria-label="Position in the recorded track"
            aria-valuetext={current ? formatTime(current.timestamp) : undefined}
          />

          <div className="pb-foot">
            <span>
              {index + 1} / {points.length} points
            </span>
            <span>
              {points[0] ? formatTime(points[0].timestamp) : '--:--'} →{' '}
              {points[last] ? formatTime(points[last].timestamp) : '--:--'}
            </span>
          </div>
        </>
      )}

      {points.length === 0 && !error && !loading && (
        <p className="pb-hint">Pick a day and load the history of this unit.</p>
      )}
    </div>
  )
}
