import { useEffect, useRef } from 'react'
import { Marker, Popup } from 'react-leaflet'
import L from 'leaflet'
import type { Vehicle } from '../lib/types'
import { vehicleTopSvg } from './VehicleArt'
import {
  formatLastSeen,
  hasPosition,
  normalizeVehicleKind,
  normalizeVehicleStatus,
  vehicleStatusColor,
  vehicleKindLabel,
  vehicleStatusLabel,
} from '../lib/types'

const esc = (t: string) => t.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)

/**
 * Monochrome badge carrying the kind glyph, with a small status dot. Icons are
 * cached by look: handing Leaflet a new icon on every position frame would
 * rebuild the DOM node and kill the CSS glide between two positions.
 */
const iconCache = new Map<string, L.DivIcon>()

/**
 * Premium marker: a glossy rounded "pin card" (gradient badge + kind glyph) that
 * sits on a pulsing radar ring, with a heading chevron, a status dot and a
 * floating plate/speed chip. The selected unit grows and shows its name.
 */
function vehicleIcon(veh: Vehicle, selected: boolean): L.DivIcon {
  const kind = normalizeVehicleKind(veh.kind)
  const status = normalizeVehicleStatus(veh.status)
  const speed = status === 'moving' ? Math.round(veh.speed_kmh) : 0
  const key = `${kind}|${status}|${selected ? esc(veh.name) : ''}|${selected ? esc(veh.code) : ''}|${selected ? speed : ''}`
  const hit = iconCache.get(key)
  if (hit) return hit

  const w = selected ? 64 : 50
  const cls = `veh ${kind} ${status}${status === 'moving' ? ' is-moving' : ''}${selected ? ' selected' : ''}`
  const chip = selected
    ? `<span class="veh-label"><b>${esc(veh.name)}</b><em>${esc(veh.code)}${
        status === 'moving' && speed > 0 ? ` · ${speed} km/h` : ''
      }</em></span>`
    : ''
  const icon = L.divIcon({
    className: 'veh-wrap',
    html:
      `<span class="${cls}" style="--veh:${vehicleStatusColor(status)}">` +
      `<s class="veh-ground"></s><s class="veh-lock"></s>` +
      `<span class="veh-body"><s class="veh-beam"></s><s class="veh-trail"></s>${vehicleTopSvg(kind)}</span>${chip}</span>`,
    iconSize: [w, w],
    iconAnchor: [w / 2, w / 2],
    popupAnchor: [0, -w / 2 + 6],
  })
  if (iconCache.size > 200) iconCache.clear()
  iconCache.set(key, icon)
  return icon
}


/** Compass bearing in degrees between two reported positions. */
function bearing(a: { lat: number; lng: number }, b: { lat: number; lng: number }): number {
  const r = (d: number) => (d * Math.PI) / 180
  const y = Math.sin(r(b.lng - a.lng)) * Math.cos(r(b.lat))
  const x = Math.cos(r(a.lat)) * Math.sin(r(b.lat)) - Math.sin(r(a.lat)) * Math.cos(r(b.lat)) * Math.cos(r(b.lng - a.lng))
  return (Math.atan2(y, x) * 180) / Math.PI
}

/** The compass point a bearing points at, e.g. 90° is "E". */
function compassPoint(deg: number): string {
  const points = ['N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW']
  const index = Math.round((((deg % 360) + 360) % 360) / 45) % 8
  return points[index]
}

/**
 * One live marker. The heading cone turns toward the direction of travel.
 *
 * A reported heading is used when the unit sends one: it is the truth about
 * where the vehicle is pointing, even when it stands still or turns on the
 * spot. Without one, the direction between the last two positions is the
 * fallback. The angle is unwrapped, so a turn from 350° to 10° rotates 20°,
 * not 340°.
 */
function VehicleMarker({
  v,
  selected,
  onSelect,
  drivable,
  onDrive,
}: {
  v: Vehicle & { lat: number; lng: number }
  selected: boolean
  onSelect: (id: number | null) => void
  /** Handing the unit a new position is the "drive" gesture: drag the marker. */
  drivable: boolean
  onDrive: (id: number, lat: number, lng: number) => void
}) {
  const ref = useRef<L.Marker | null>(null)
  const prev = useRef<{ lat: number; lng: number } | null>(null)
  const heading = useRef<number | null>(null)
  const status = normalizeVehicleStatus(v.status)

  useEffect(() => {
    const p = prev.current
    if (p) {
      const dy = (v.lat - p.lat) * 111320
      const dx = (v.lng - p.lng) * 111320 * Math.cos((v.lat * Math.PI) / 180)
      if (Math.hypot(dx, dy) > 3) {
        const next = bearing(p, v)
        if (heading.current === null) heading.current = next
        else heading.current += ((next - heading.current + 540) % 360) - 180
        prev.current = { lat: v.lat, lng: v.lng }
      }
    } else {
      prev.current = { lat: v.lat, lng: v.lng }
    }
    // Leaflet rebuilds the icon element when the look changes, so re-apply every time.
    const el = ref.current?.getElement()
    if (el) {
      // Leaflet copies options.title onto the icon element only while building it,
      // so a renamed unit would keep the old tooltip until the next rebuild.
      el.setAttribute('title', `${v.code} · ${v.name}`)
    }
    // The reported heading wins over the one derived from the movement.
    const shown = typeof v.heading === 'number' && Number.isFinite(v.heading) ? v.heading : heading.current
    if (el && shown !== null) {
      el.style.setProperty('--hd', `${shown.toFixed(1)}deg`)
      el.style.setProperty('--hd-on', '1')
    }
  }, [v.lat, v.lng, v.heading, selected, status, v])

  return (
    <Marker
      ref={ref}
      position={[v.lat, v.lng]}
      icon={vehicleIcon(v, selected)}
      zIndexOffset={selected ? 1400 : 700}
      draggable={drivable}
      eventHandlers={{
        click: () => onSelect(v.id),
        dragend: (e) => {
          const next = (e.target as L.Marker).getLatLng()
          onDrive(v.id, next.lat, next.lng)
        },
      }}
      keyboard
      title={`${v.code} · ${v.name}`}
    >
      <Popup className="veh-popup" closeButton={false} offset={[0, -6]}>
        <span className="vp-head">
          <span className="vp-badge" style={{ background: vehicleStatusColor(status) }}>
            {vehicleStatusLabel(v.status)}
          </span>
          <span className="vp-code">{v.code}</span>
        </span>
        <strong className="popup-title">{v.name}</strong>
        <span className="popup-meta">{vehicleKindLabel(v.kind)}</span>
        <span className="vp-grid">
          <span>
            <small>Speed</small>
            <b>{status === 'moving' ? Math.round(v.speed_kmh) : 0}<i> km/h</i></b>
          </span>
          <span>
            <small>Heading</small>
            <b>
              {typeof v.heading === 'number' && Number.isFinite(v.heading)
                ? `${Math.round(v.heading)}° ${compassPoint(v.heading)}`
                : '—'}
            </b>
          </span>
          <span>
            <small>Updated</small>
            <b>{formatLastSeen(v.last_seen_at)}</b>
          </span>
        </span>
        <span className="popup-coords">
          {v.lat.toFixed(5)}, {v.lng.toFixed(5)}
        </span>
      </Popup>
    </Marker>
  )
}

interface Props {
  vehicles: Vehicle[]
  selectedId: number | null
  onSelect: (id: number | null) => void
  /** Markers can be hidden without dropping the live feed. */
  visible: boolean
  /** True when this account may move the unit by dragging its marker. */
  canDrive: (v: Vehicle) => boolean
  onDrive: (id: number, lat: number, lng: number) => void
}

/**
 * Live vehicle markers, drawn above the zones and the administrative
 * boundaries. Units that have not reported a position yet are skipped.
 */
export default function VehicleLayer({
  vehicles,
  selectedId,
  onSelect,
  visible,
  canDrive,
  onDrive,
}: Props) {
  if (!visible) return null

  const located = vehicles.filter(hasPosition)

  return (
    <>
      {located.map((v) => (
        <VehicleMarker
          key={v.id}
          v={v}
          selected={v.id === selectedId}
          onSelect={onSelect}
          drivable={canDrive(v)}
          onDrive={onDrive}
        />
      ))}
    </>
  )
}
