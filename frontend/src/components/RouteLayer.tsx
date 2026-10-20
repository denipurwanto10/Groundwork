import { useEffect, useMemo, useRef } from 'react'
import type { ComponentProps } from 'react'
import { Marker, Polyline as RLPolyline, Popup, Tooltip } from 'react-leaflet'
import L from 'leaflet'
import type { RouteStop, VehicleRoute } from '../lib/types'
import { formatDistance, formatDuration, routeLatLngs, routeProgress } from '../lib/types'

/**
 * The planned delivery path of every vehicle, drawn over the base map:
 * a solid line for the part still ahead, a muted dashed line for the part
 * already covered, plus A/B endpoints and a distance/duration label.
 *
 * Progress comes from the server (it projects the last reported position onto
 * the line with ST_LineLocatePoint), so the split here is just geometry.
 */


function stopIcon(kind: 'A' | 'B', label: string): L.DivIcon {
  return L.divIcon({
    className: 'route-stop-wrap',
    html: `<span class="route-stop ${kind === 'A' ? 'origin' : 'dest'}"><b>${kind}</b><i>${label}</i></span>`,
    iconSize: [28, 28],
    iconAnchor: [14, 14],
  })
}


/**
 * Polyline whose SVG path carries a CSS class. Leaflet only reads `className`
 * when the path is created and react-leaflet restyles through setStyle, so the
 * class is added to the element itself; colours then come from CSS variables
 * (see modern.css) and follow the light / dark theme.
 */
function Polyline({ cls, ...props }: ComponentProps<typeof RLPolyline> & { cls: string }) {
  const ref = useRef<L.Polyline | null>(null)
  useEffect(() => {
    const el = ref.current?.getElement()
    if (el) el.classList.add(cls)
  })
  return <RLPolyline ref={ref} {...props} />
}

/** Great-circle distance in metres, used only to split one line in two. */
function haversine(a: [number, number], b: [number, number]): number {
  const R = 6371008.8
  const toRad = (d: number) => (d * Math.PI) / 180
  const dLat = toRad(b[0] - a[0])
  const dLng = toRad(b[1] - a[1])
  const lat1 = toRad(a[0])
  const lat2 = toRad(b[0])
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(lat1) * Math.cos(lat2) * Math.sin(dLng / 2) ** 2
  return 2 * R * Math.asin(Math.min(1, Math.sqrt(h)))
}

/**
 * Splits the polyline at `fraction` (0..1 of its total length) so the covered
 * part and the remaining part can be styled differently. The cut point is
 * interpolated inside the segment it falls in, so the split does not jump
 * between vertices.
 */
function splitAt(points: [number, number][], fraction: number): { done: [number, number][]; rest: [number, number][] } {
  if (points.length < 2) return { done: [], rest: points }
  if (fraction <= 0) return { done: [], rest: points }
  if (fraction >= 1) return { done: points, rest: [] }

  const cum: number[] = [0]
  for (let i = 1; i < points.length; i++) cum.push(cum[i - 1] + haversine(points[i - 1], points[i]))
  const total = cum[cum.length - 1]
  if (total <= 0) return { done: [], rest: points }

  const target = total * fraction
  let i = 1
  while (i < cum.length && cum[i] < target) i++
  if (i >= points.length) return { done: points, rest: [] }

  const segStart = cum[i - 1]
  const segLen = cum[i] - segStart
  const t = segLen > 0 ? (target - segStart) / segLen : 0
  const cut: [number, number] = [
    points[i - 1][0] + (points[i][0] - points[i - 1][0]) * t,
    points[i - 1][1] + (points[i][1] - points[i - 1][1]) * t,
  ]

  return { done: [...points.slice(0, i), cut], rest: [cut, ...points.slice(i)] }
}

/** Compass bearing in degrees from one point to the next, for the heading arrow. */
function bearingDeg(from: [number, number], to: [number, number]): number {
  const toRad = (d: number) => (d * Math.PI) / 180
  const φ1 = toRad(from[0])
  const φ2 = toRad(to[0])
  const Δλ = toRad(to[1] - from[1])
  const y = Math.sin(Δλ) * Math.cos(φ2)
  const x = Math.cos(φ1) * Math.sin(φ2) - Math.sin(φ1) * Math.cos(φ2) * Math.cos(Δλ)
  return (Math.atan2(y, x) * 180) / Math.PI
}

/** The live puck: a dot at the head of the line, arrow turned along the road. */
function puckIcon(deg: number): L.DivIcon {
  return L.divIcon({
    className: 'route-puck-wrap',
    html: `<span class="route-puck"><i style="transform:rotate(${deg.toFixed(0)}deg)"></i></span>`,
    iconSize: [20, 20],
    iconAnchor: [10, 10],
  })
}

function RouteLine({
  route,
  onSelectVehicle,
}: {
  route: VehicleRoute
  onSelectVehicle: (id: number | null) => void
}) {
  const points = useMemo(() => routeLatLngs(route), [route])
  const progress = routeProgress(route)
  const { done, rest } = useMemo(() => splitAt(points, progress), [points, progress])

  if (points.length < 2) return null

  const origin = route.origin
  const destination = route.destination
  const summary = `${origin.name || 'Origin'} → ${destination.name || 'Destination'}`
  const detail = `${formatDistance(route.distance_m)} · ${formatDuration(route.duration_s)} · ${
    route.source === 'osrm' ? 'driving route' : 'straight line'
  }`

  return (
    <>
      {done.length > 1 && (
        <Polyline cls="route-done" positions={done} pathOptions={{ weight: 6, opacity: 1, dashArray: '1 11', lineCap: 'round' }} />
      )}
      {done.length > 1 && rest.length > 1 && (
        <Marker
          position={done[done.length - 1]}
          icon={puckIcon(bearingDeg(done[done.length - 1], rest[1]))}
          interactive={false}
          keyboard={false}
        />
      )}
      {rest.length > 1 && (
        <Polyline cls="route-casing" positions={rest} pathOptions={{ weight: 13, opacity: 1, lineCap: 'round', lineJoin: 'round' }} interactive={false} />
      )}
      {rest.length > 1 && (
        <Polyline
          cls="route-ahead"
          positions={rest}
          pathOptions={{ weight: 7, opacity: 1, lineCap: 'round', lineJoin: 'round' }}
          eventHandlers={{ click: () => onSelectVehicle(route.vehicle_id) }}
        >
          <Tooltip sticky>
            <strong>{summary}</strong>
            <br />
            <span>{detail}</span>
          </Tooltip>
        </Polyline>
      )}
      <Marker position={[origin.lat, origin.lng]} icon={stopIcon('A', origin.name || 'Start')} keyboard={false}>
        <Popup>
          <strong className="popup-title">{origin.name || 'Origin'}</strong>
          <span className="popup-coords">
            {origin.lat.toFixed(5)}, {origin.lng.toFixed(5)}
          </span>
        </Popup>
      </Marker>
      <Marker position={[destination.lat, destination.lng]} icon={stopIcon('B', destination.name || 'Finish')} keyboard={false}>
        <Popup>
          <strong className="popup-title">{destination.name || 'Destination'}</strong>
          <span className="popup-coords">
            {destination.lat.toFixed(5)}, {destination.lng.toFixed(5)}
          </span>
          <span className="popup-meta">{detail}</span>
        </Popup>
      </Marker>
    </>
  )
}

interface Props {
  routes: VehicleRoute[]
  visible: boolean
  /** Stops picked for a route that has not been saved yet. */
  draft: { origin: RouteStop | null; destination: RouteStop | null } | null
  onSelectVehicle: (id: number | null) => void
}

export default function RouteLayer({ routes, visible, draft, onSelectVehicle }: Props) {
  if (!visible) return null

  return (
    <>
      {routes
        .filter((r) => r.status === 'active')
        .map((r) => (
          <RouteLine key={r.id} route={r} onSelectVehicle={onSelectVehicle} />
        ))}
      {draft?.origin && (
        <Marker position={[draft.origin.lat, draft.origin.lng]} icon={stopIcon('A', draft.origin.name || 'Origin')} keyboard={false} />
      )}
      {draft?.destination && (
        <Marker
          position={[draft.destination.lat, draft.destination.lng]}
          icon={stopIcon('B', draft.destination.name || 'Destination')}
          keyboard={false}
        />
      )}
    </>
  )
}
