import { memo, useCallback, useEffect, useMemo, useRef } from 'react'
import { Marker, Polyline, useMap } from 'react-leaflet'
import L from 'leaflet'
import type { HistoryPoint, VehicleKind } from '../lib/types'
import { normalizeVehicleKind } from '../lib/types'
import { vehicleTopSvg } from './VehicleArt'
import type { PlaybackCursor } from '../lib/playback'

/** How often the trail is trimmed while the track runs, in milliseconds. */
const TRAIL_PAINT_MS = 80

/**
 * The recorded trip of a unit, drawn on the map: the track as a thick line and
 * a ghost marker that walks along it.
 *
 * The marker is moved imperatively, straight on the Leaflet object: the
 * animation runs at the frame rate of the screen, and the app is not
 * re-rendered for every step.
 *
 * The track is drawn in two parts: the trail the unit has already left is
 * solid, the rest of the recorded day is faint. The ghost stands at the head
 * of the solid part, so the vehicle is always ahead of the path it drove.
 */

/** The ghost marker: the vehicle art, half transparent. */
function ghostIcon(kind: VehicleKind): L.DivIcon {
  return L.divIcon({
    className: 'veh-wrap pb-ghost',
    html:
      `<span class="veh ${kind}" style="--veh:#5b6472">` +
      `<s class="veh-ground"></s>` +
      `<span class="veh-body">${vehicleTopSvg(kind)}</span></span>`,
    iconSize: [46, 46],
    iconAnchor: [23, 23],
  })
}

/**
 * The part of the track the unit has driven: every point before the cursor,
 * plus the cursor itself, so the trail ends exactly where the ghost stands.
 */
function drivenLatLngs(points: HistoryPoint[], cursor: PlaybackCursor): [number, number][] {
  const at = Math.max(0, Math.min(points.length - 1, Math.floor(cursor.index)))
  const out: [number, number][] = []
  for (let i = 0; i < at; i++) out.push([points[i].lat, points[i].lng])
  out.push([cursor.lat, cursor.lng])
  return out
}

interface Props {
  /** The recorded positions, oldest first. */
  points: HistoryPoint[]
  /** Where the ghost marker stands right now. */
  cursor: React.MutableRefObject<PlaybackCursor>
  /** True while the track is running (the marker glides between points). */
  playing: boolean
  /** Index of the point the playback stands at; a seek moves the marker. */
  index: number
  /** The kind of vehicle, so the ghost looks like the unit it stands for. */
  kind: VehicleKind
  /** The unit the track belongs to; the ghost is hidden when it is not shown. */
  visible: boolean
}

/**
 * The track of the unit. It is a separate component so the line is built
 * once: the line does not change while the ghost walks along it.
 *
 * The solid line is the trail the unit has left; it is trimmed by hand while
 * the playback runs, so it always ends at the ghost.
 */
const HistoryTrack = memo(function HistoryTrack({
  points,
  trailRef,
}: {
  points: HistoryPoint[]
  trailRef: React.MutableRefObject<L.Polyline | null>
}) {
  const positions = useMemo<[number, number][]>(
    () => points.map((p) => [p.lat, p.lng] as [number, number]),
    [points],
  )
  // The trail starts empty: the effect below paints the part that is driven.
  const start = useMemo<[number, number][]>(() => positions.slice(0, 2), [positions])
  if (positions.length < 2) return null

  return (
    <>
      {/* A pale halo under the line, so the track stays readable over the
          map and over the zones. */}
      <Polyline
        positions={positions}
        pathOptions={{ color: '#ffffff', weight: 9, opacity: 0.25, lineCap: 'round', lineJoin: 'round' }}
        interactive={false}
      />
      {/* The recorded day, faint: the part of the path the unit has not
          walked yet in the playback. */}
      <Polyline
        positions={positions}
        pathOptions={{ color: '#8d97a8', weight: 3, opacity: 0.4, lineCap: 'round', lineJoin: 'round' }}
        interactive={false}
      />
      {/* The trail the unit has left. It is history, not a live route, so it
          stays quiet next to the coloured routes. */}
      <Polyline
        ref={trailRef}
        positions={start}
        pathOptions={{ color: '#8d97a8', weight: 4, opacity: 0.9, lineCap: 'round', lineJoin: 'round' }}
        interactive={false}
      />
    </>
  )
})

export default function PlaybackLayer({ points, cursor, playing, index, kind, visible }: Props) {
  const map = useMap()
  const markerRef = useRef<L.Marker | null>(null)
  const trailRef = useRef<L.Polyline | null>(null)
  const icon = useMemo(() => ghostIcon(normalizeVehicleKind(kind)), [kind])

  /** Paints the trail so that it ends where the ghost stands. */
  const paintTrail = useCallback(
    (c: PlaybackCursor) => {
      trailRef.current?.setLatLngs(drivenLatLngs(points, c))
    },
    [points],
  )

  // The camera follows the track: without it the ghost would drive somewhere
  // the operator cannot see. The margin is bigger than the rest: the playback
  // dialog sits in the corner of the map and would otherwise hide the track.
  useEffect(() => {
    if (points.length < 2) return
    const bounds = L.latLngBounds(points.map((p) => [p.lat, p.lng] as [number, number]))
    // On a wide screen the dialog sits in the lower right corner, on a phone
    // it lies along the bottom of the map. The margins follow the size of the
    // map: a fixed margin would be bigger than the map itself on a narrow
    // window, and the track would not fit at all.
    const size = map.getSize()
    const wide = window.matchMedia('(min-width: 768px)').matches
    const right = wide ? Math.min(420, Math.max(60, size.x * 0.42)) : 56
    const bottom = Math.min(240, Math.max(60, size.y * 0.42))
    map.fitBounds(bounds, {
      paddingTopLeft: [56, 56],
      paddingBottomRight: [right, bottom],
      maxZoom: 15,
    })
  }, [map, points])

  // The ghost marker follows the cursor. While the track runs, the cursor
  // moves on its own frame loop, so the marker is moved there too; when the
  // operator drags the slider, the cursor jumps and the marker follows.
  useEffect(() => {
    if (!playing) return
    let frame = 0
    let painted = 0
    const step = (now: number) => {
      const c = cursor.current
      markerRef.current?.setLatLng([c.lat, c.lng])
      // The trail is trimmed a few times per second: it ends at the ghost,
      // and rebuilding the whole line every frame would cost more than it
      // shows.
      if (now - painted >= TRAIL_PAINT_MS) {
        painted = now
        paintTrail(c)
      }
      frame = requestAnimationFrame(step)
    }
    frame = requestAnimationFrame(step)
    return () => cancelAnimationFrame(frame)
  }, [playing, cursor, paintTrail])

  // A seek while the track is paused has to move the marker too. The index
  // is what changes when the operator drags the slider.
  useEffect(() => {
    if (playing) return
    const c = cursor.current
    markerRef.current?.setLatLng([c.lat, c.lng])
    paintTrail(c)
  }, [playing, cursor, points, index, paintTrail])

  // The position the marker is created with. It is memoized on purpose:
  // react-leaflet moves the marker whenever the position prop is a new
  // array, and the ghost is moved by hand while the track runs.
  const start = useMemo<[number, number]>(
    () => [points[0]?.lat ?? 0, points[0]?.lng ?? 0],
    [points],
  )

  if (!visible || points.length === 0) return null

  return (
    <>
      <HistoryTrack points={points} trailRef={trailRef} />
      <Marker
        ref={markerRef}
        position={start}
        icon={icon}
        zIndexOffset={900}
        interactive={false}
        keyboard={false}
        title="Recorded position"
      />
    </>
  )
}
