/**
 * Playback of a recorded trip.
 *
 * The live feed says where a unit is now. The history says where it was:
 * the server hands out the positions it reported, and this hook replays them
 * on the map.
 *
 * The trick is the cursor. The animation runs on its own frame loop and keeps
 * the exact position of the ghost marker in a ref, so the marker glides
 * smoothly without re-rendering the app sixty times a second. React only
 * hears about the index a few times per second, which is all the slider and
 * the time readout need.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from './api'
import type { HistoryPoint, PlaybackSpeed } from './types'
import { PLAYBACK_SPEEDS } from './types'

/** How long the whole track takes at 1x, whatever the number of points. */
const REPLAY_SECONDS = 40

/** How often React is told about the current index, in milliseconds. */
const REPORT_INTERVAL_MS = 100

/**
 * Cursor is where the playback stands right now: the index in the track (a
 * fraction, so the marker can be between two points) and the position the
 * marker is drawn at.
 */
export interface PlaybackCursor {
  index: number
  lat: number
  lng: number
}

/** What the playback panel and the map layer need. */
export interface PlaybackController {
  /** The unit whose history is loaded, or null when the playback is closed. */
  vehicleId: number | null
  /** The day the history is read from, YYYY-MM-DD. */
  date: string
  /** The recorded positions, oldest first. */
  data: HistoryPoint[]
  /** Index of the point the playback stands at. */
  index: number
  /** True while the track is running. */
  playing: boolean
  /** How fast the track runs. */
  speed: number
  loading: boolean
  error: string | null
  /** The live position of the ghost marker. */
  cursor: React.MutableRefObject<PlaybackCursor>
  open(vehicleId: number): void
  close(): void
  load(): Promise<void>
  toggle(): void
  seek(index: number): void
  setDate(date: string): void
  setSpeed(speed: number): void
}

/** Today, in the format the date input and the API expect. */
export function todayISO(now = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

export function usePlayback(): PlaybackController {
  const [vehicleId, setVehicleId] = useState<number | null>(null)
  const [date, setDate] = useState(() => todayISO())
  const [data, setData] = useState<HistoryPoint[]>([])
  const [index, setIndex] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState<PlaybackSpeed>(1)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // The exact position of the ghost marker. The map layer reads it every
  // frame; React never has to.
  const cursor = useRef<PlaybackCursor>({ index: 0, lat: 0, lng: 0 })

  // Open the playback for a unit: the track of the previous day is dropped,
  // the new one starts empty.
  const open = useCallback((id: number) => {
    setVehicleId(id)
    setData([])
    setIndex(0)
    setPlaying(false)
    setError(null)
    setDate(todayISO())
    cursor.current = { index: 0, lat: 0, lng: 0 }
  }, [])

  const close = useCallback(() => {
    setPlaying(false)
    setVehicleId(null)
    setData([])
    setIndex(0)
    setError(null)
    cursor.current = { index: 0, lat: 0, lng: 0 }
  }, [])

  const load = useCallback(async () => {
    if (vehicleId == null) return
    setLoading(true)
    setError(null)
    setPlaying(false)
    try {
      const points = await api.vehicleHistory(vehicleId, { date })
      setData(points)
      setIndex(0)
      cursor.current = {
        index: 0,
        lat: points[0]?.lat ?? 0,
        lng: points[0]?.lng ?? 0,
      }
      if (points.length === 0) {
        setError('No positions were recorded for that day.')
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load the history')
    } finally {
      setLoading(false)
    }
  }, [vehicleId, date])

  // Seek moves the ghost to a point of the track.
  const seek = useCallback(
    (next: number) => {
      if (data.length === 0) return
      const clamped = Math.max(0, Math.min(data.length - 1, next))
      setIndex(clamped)
      cursor.current = {
        index: clamped,
        lat: data[clamped].lat,
        lng: data[clamped].lng,
      }
    },
    [data],
  )

  const toggle = useCallback(() => {
    if (playing) {
      setPlaying(false)
      return
    }
    // A finished track starts again from the beginning.
    if (data.length > 0 && index >= data.length - 1) {
      seek(0)
    }
    setPlaying(true)
  }, [playing, data.length, index, seek])

  // Only the listed speeds exist; anything else falls back to 1x.
  const changeSpeed = useCallback((value: number) => {
    const allowed = PLAYBACK_SPEEDS.find((s) => s === value)
    setSpeed(allowed ?? 1)
  }, [])

  // The animation: the cursor walks the track, the ghost follows it.
  useEffect(() => {
    if (!playing || data.length === 0) return

    let frame = 0
    let last = performance.now()
    let reported = last
    const perSecond = Math.max(1, data.length / REPLAY_SECONDS)

    const step = (now: number) => {
      // The frame callback carries the time the frame started, which can be
      // before the effect was set up, and a tab that was in the background
      // comes back with a huge delta. Both are clamped, so the track neither
      // walks backwards nor jumps to the end in one frame.
      const dt = Math.max(0, Math.min(0.25, (now - last) / 1000))
      last = now

      const current = cursor.current.index
      const next = current + perSecond * speed * dt
      const lastIndex = data.length - 1

      if (next >= lastIndex) {
        const end = data[lastIndex]
        cursor.current = { index: lastIndex, lat: end.lat, lng: end.lng }
        setIndex(lastIndex)
        setPlaying(false)
        return
      }

      // The ghost sits between two points while the cursor walks from one to
      // the other, so it never teleports.
      const i = Math.max(0, Math.min(lastIndex - 1, Math.floor(next)))
      const from = data[i]
      const to = data[i + 1]
      const fraction = next - i
      cursor.current = {
        index: next,
        lat: from.lat + (to.lat - from.lat) * fraction,
        lng: from.lng + (to.lng - from.lng) * fraction,
      }

      if (now - reported >= REPORT_INTERVAL_MS) {
        reported = now
        setIndex(i)
      }
      frame = requestAnimationFrame(step)
    }

    frame = requestAnimationFrame(step)
    return () => cancelAnimationFrame(frame)
  }, [playing, data, speed])

  return {
    vehicleId,
    date,
    data,
    index,
    playing,
    speed,
    loading,
    error,
    cursor,
    open,
    close,
    load,
    toggle,
    seek,
    setDate,
    setSpeed: changeSpeed,
  }
}
