import { useEffect, useRef } from 'react'
import { Marker, Popup } from 'react-leaflet'
import L from 'leaflet'
import type { Vehicle } from '../lib/types'
import {
  formatLastSeen,
  hasPosition,
  normalizeVehicleKind,
  normalizeVehicleStatus,
  vehicleStatusColor,
  vehicleKindLabel,
  vehicleStatusLabel,
} from '../lib/types'

/**
 * Inline glyph per kind. Leaflet builds the marker from an HTML string, so the
 * shapes live here as SVG markup with no extra icon files to ship.
 */
const KIND_GLYPH: Record<string, string> = {
  truck:
    '<svg viewBox="0 0 24 24"><path d="M2.5 16.5V7h10.5v9.5"/><path d="M13 10.5h4.2L20.5 14v2.5h-7.5"/><circle cx="6.5" cy="17.5" r="1.7"/><circle cx="17" cy="17.5" r="1.7"/></svg>',
  van: '<svg viewBox="0 0 24 24"><path d="M2.5 16.5V9h9.5v7.5"/><path d="M12 16.5h9.5V13L18 9h-6"/><circle cx="6.5" cy="17.5" r="1.6"/><circle cx="16.5" cy="17.5" r="1.6"/></svg>',
  motorcycle:
    '<svg viewBox="0 0 24 24"><circle cx="5.5" cy="16.5" r="2.6"/><circle cx="18.5" cy="16.5" r="2.6"/><path d="M8 16.5l3.5-6h4l3 6"/><path d="M9.5 10.5h5"/></svg>',
  pickup:
    '<svg viewBox="0 0 24 24"><path d="M2.5 16.5V11h7.5v5.5"/><path d="M10 16.5h10.5V13h-5.5V11"/><circle cx="6.5" cy="17.5" r="1.6"/><circle cx="16.5" cy="17.5" r="1.6"/></svg>',
}

const esc = (t: string) => t.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)

/**
 * Monochrome badge carrying the kind glyph, with a small status dot. Icons are
 * cached by look: handing Leaflet a new icon on every position frame would
 * rebuild the DOM node and kill the CSS glide between two positions.
 */
const iconCache = new Map<string, L.DivIcon>()

function vehicleIcon(veh: Vehicle, selected: boolean): L.DivIcon {
  const kind = normalizeVehicleKind(veh.kind)
  const status = normalizeVehicleStatus(veh.status)
  const key = `${kind}|${status}|${selected ? esc(veh.name) : ''}`
  const hit = iconCache.get(key)
  if (hit) return hit

  const size = selected ? 48 : 40
  const cls = `veh ${status}${status === 'moving' ? ' is-moving' : ''}${selected ? ' selected' : ''}`
  const glyph = KIND_GLYPH[kind] ?? KIND_GLYPH.truck
  const label = selected ? `<span class="veh-label">${esc(veh.name)}</span>` : ''
  const icon = L.divIcon({
    className: 'veh-wrap',
    html: `<span class="${cls}" style="--veh:${vehicleStatusColor(status)}"><s class="veh-heading"></s><i class="veh-glyph">${glyph}</i><b class="veh-dot"></b>${label}</span>`,
    iconSize: [size, size],
    iconAnchor: [size / 2, size / 2],
    popupAnchor: [0, -size / 2 + 2],
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

/**
 * One live marker. The heading cone turns toward the direction of travel,
 * derived from the last two positions (the feed carries no heading). The angle
 * is unwrapped so a turn from 350° to 10° rotates 20°, not 340°.
 */
function VehicleMarker({
  v,
  selected,
  onSelect,
}: {
  v: Vehicle & { lat: number; lng: number }
  selected: boolean
  onSelect: (id: number | null) => void
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
    if (el && heading.current !== null) {
      el.style.setProperty('--hd', `${heading.current.toFixed(1)}deg`)
      el.style.setProperty('--hd-on', '1')
    }
  }, [v.lat, v.lng, selected, status, v])

  return (
    <Marker
      ref={ref}
      position={[v.lat, v.lng]}
      icon={vehicleIcon(v, selected)}
      zIndexOffset={selected ? 1400 : 700}
      eventHandlers={{ click: () => onSelect(v.id) }}
      keyboard
      title={`${v.code} · ${v.name}`}
    >
      <Popup>
        <strong className="popup-title">{v.name}</strong>
        <span className="popup-meta">
          {vehicleKindLabel(v.kind)} · {vehicleStatusLabel(v.status)}
          {status === 'moving' && v.speed_kmh > 0 ? ` · ${Math.round(v.speed_kmh)} km/h` : ''}
        </span>
        <span className="popup-coords">
          {v.lat.toFixed(5)}, {v.lng.toFixed(5)}
        </span>
        <span className="popup-coords">
          {v.code} · {formatLastSeen(v.last_seen_at)}
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
}

/**
 * Live vehicle markers, drawn above the zones and the administrative
 * boundaries. Units that have not reported a position yet are skipped.
 */
export default function VehicleLayer({ vehicles, selectedId, onSelect, visible }: Props) {
  if (!visible) return null

  const located = vehicles.filter(hasPosition)

  return (
    <>
      {located.map((v) => (
        <VehicleMarker key={v.id} v={v} selected={v.id === selectedId} onSelect={onSelect} />
      ))}
    </>
  )
}
