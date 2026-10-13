import { useEffect } from 'react'
import {
  AttributionControl,
  MapContainer,
  Marker,
  Popup,
  TileLayer,
  ZoomControl,
  useMap,
  useMapEvents,
} from 'react-leaflet'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import type { Entity } from '../lib/types'
import { typeColorFor } from '../lib/types'

// Custom CSS pin markers (no bundled PNGs needed); colour follows entity type
// (fixed palette → hash fallback), inactive is the same hue dimmed, new point is black.
function pinIcon(status: 'active' | 'inactive' | 'draft', type: string, selected = false): L.DivIcon {
  const size = selected ? 46 : 36
  const color = status === 'draft' ? '#111827' : typeColorFor(type)
  return L.divIcon({
    className: 'pin-wrap',
    html: `<span class="pin ${status}${selected ? ' selected' : ''}" style="--pin:${color}"><i></i></span>`,
    iconSize: [size, size],
    iconAnchor: [size / 2, size - 2],
    popupAnchor: [0, -size + 6],
  })
}

const JAKARTA: [number, number] = [-6.2, 106.85]

// OpenStreetMap standard tiles need no API key. Dark mode is done with a CSS filter
// (.tiles-dark in App.css). To use another provider, set VITE_MAP_TILES_URL.
const TILE_URL =
  (import.meta.env.VITE_MAP_TILES_URL as string | undefined) || 'https://tile.openstreetmap.org/{z}/{x}/{y}.png'

function ClickPicker({ picking, onPick }: { picking: boolean; onPick: (lat: number, lng: number) => void }) {
  useMapEvents({
    click(e) {
      if (picking) onPick(e.latlng.lat, e.latlng.lng)
    },
  })
  return null
}

/** "Show all" button under the zoom control; flies to the bounds of all entities. */
function FitAllControl({ entities }: { entities: Entity[] }) {
  const map = useMap()
  useEffect(() => {
    if (entities.length === 0) return
    const Control = L.Control.extend({
      onAdd() {
        const btn = L.DomUtil.create('button', 'fit-all-btn')
        btn.type = 'button'
        btn.title = 'Show all entities'
        btn.setAttribute('aria-label', 'Show all entities')
        btn.textContent = '⤢'
        L.DomEvent.on(btn, 'click', (e) => {
          L.DomEvent.stop(e)
          const bounds = L.latLngBounds(entities.map((x) => [x.latitude, x.longitude] as [number, number]))
          if (bounds.isValid()) map.flyToBounds(bounds.pad(0.15), { duration: 0.6 })
        })
        return btn
      },
    })
    const ctrl = new Control({ position: 'topright' })
    ctrl.addTo(map)
    return () => {
      ctrl.remove()
    }
  }, [map, entities])
  return null
}

/** Pan to the selected entity, keeping the pin clear of the detail card / bottom sheet. */
function FlyToSelected({ entity }: { entity: Entity | undefined }) {
  const map = useMap()
  useEffect(() => {
    if (!entity) return
    const zoom = Math.max(map.getZoom(), 13)
    const target = L.latLng(entity.latitude, entity.longitude)
    const wide = window.matchMedia('(min-width: 768px)').matches
    // Lift the pin a little so it clears the detail card (desktop) or bottom sheet (phone).
    const dy = wide ? 60 : Math.round(window.innerHeight * 0.17)
    const centre = map.unproject(map.project(target, zoom).add([0, dy]), zoom)
    map.flyTo(centre, zoom, { duration: 0.6 })
  }, [entity?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  return null
}

interface Props {
  entities: Entity[]
  selected: Entity | undefined
  onSelect: (id: number | null) => void
  picking: boolean
  draftCoords: { latitude: number; longitude: number } | null
  onPick: (lat: number, lng: number) => void
  theme: 'light' | 'dark'
}

export default function EntityMap({ entities, selected, onSelect, picking, draftCoords, onPick, theme }: Props) {
  const center: [number, number] =
    selected != null
      ? [selected.latitude, selected.longitude]
      : entities.length > 0
        ? [entities[0].latitude, entities[0].longitude]
        : JAKARTA

  return (
    <MapContainer
      center={center}
      zoom={entities.length > 0 || selected ? 13 : 11}
      scrollWheelZoom
      zoomControl={false}
      attributionControl={false}
      className={picking ? 'map picking' : 'map'}
    >
      <TileLayer
        key={theme}
        attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
        url={TILE_URL}
        className={theme === 'dark' ? 'tiles-dark' : undefined}
        maxZoom={19}
      />
      {entities.map((e) => (
        <Marker
          key={e.id}
          position={[e.latitude, e.longitude]}
          icon={pinIcon(e.status, e.type, e.id === selected?.id)}
          zIndexOffset={e.id === selected?.id ? 1000 : 0}
          eventHandlers={{ click: () => onSelect(e.id) }}
          keyboard
          title={e.name}
        >
          <Popup>
            <strong className="popup-title">{e.name}</strong>
            <span className="popup-meta">
              {e.type} · {e.status}
            </span>
            <span className="popup-coords">
              {e.latitude.toFixed(5)}, {e.longitude.toFixed(5)}
            </span>
          </Popup>
        </Marker>
      ))}
      {draftCoords != null && (
        <Marker position={[draftCoords.latitude, draftCoords.longitude]} icon={pinIcon('draft', '', true)}>
          <Popup>New point</Popup>
        </Marker>
      )}
      <ZoomControl position="topright" />
      <AttributionControl position="bottomright" prefix={false} />
      <FitAllControl entities={entities} />
      <ClickPicker picking={picking} onPick={onPick} />
      <FlyToSelected entity={selected} />
    </MapContainer>
  )
}
