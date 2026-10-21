import { useEffect, useRef } from 'react'
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
import '@geoman-io/leaflet-geoman-free/dist/leaflet-geoman.css'
import type { Entity, RegionCollection, RegionFeature, Vehicle, VehicleRoute, RouteStop, ZoneFeature, ZoneGeometry } from '../lib/types'
import { hasPosition, routeLatLngs, typeColorFor } from '../lib/types'
import ZoneLayer from './ZoneLayer'
import RegionLayer from './RegionLayer'
import VehicleLayer from './VehicleLayer'
import RouteLayer from './RouteLayer'

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

// OpenStreetMap standard raster tiles: free, no API key. Tinted in CSS
// (.tiles-gmaps) towards the Google Maps palette. Override the endpoint with
// VITE_MAP_TILES_URL.
const TILE_URL =
  (import.meta.env.VITE_MAP_TILES_URL as string | undefined) || 'https://tile.openstreetmap.org/{z}/{x}/{y}.png'

const MAP_ATTRIBUTION =
  '&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">OpenStreetMap</a> contributors'

function ClickPicker({
  picking,
  onPick,
  routePick,
  onRoutePoint,
}: {
  picking: boolean
  onPick: (lat: number, lng: number) => void
  routePick: boolean
  onRoutePoint: (lat: number, lng: number) => void
}) {
  useMapEvents({
    click(e) {
      // A route stop pick wins: while it is armed the map is a coordinate
      // picker for the delivery form, not for a new entity pin.
      if (routePick) onRoutePoint(e.latlng.lat, e.latlng.lng)
      else if (picking) onPick(e.latlng.lat, e.latlng.lng)
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

/**
 * The map lives in a card whose size changes with the page (nav rail, view
 * switch, phone layout). Leaflet only reads its container size on window
 * resize, so tell it whenever the card itself changes.
 */
function InvalidateOnResize() {
  const map = useMap()
  useEffect(() => {
    const el = map.getContainer()
    let frame = 0
    const ro = new ResizeObserver(() => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => map.invalidateSize({ animate: false }))
    })
    ro.observe(el)
    return () => {
      cancelAnimationFrame(frame)
      ro.disconnect()
    }
  }, [map])
  return null
}

/** Pan to the selected entity, keeping the pin clear of any inset. */
function FlyToSelected({ entity, inset }: { entity: Entity | undefined; inset: MapInset }) {
  const map = useMap()
  const insetRef = useRef(inset)
  insetRef.current = inset
  useEffect(() => {
    if (!entity) return
    const zoom = Math.max(map.getZoom(), 13)
    const target = L.latLng(entity.latitude, entity.longitude)
    const wide = window.matchMedia('(min-width: 768px)').matches
    // Lift the pin a little so it clears the detail card (desktop) or bottom sheet (phone).
    const dy = wide ? 60 : Math.round(window.innerHeight * 0.17)
    const dx = wide ? -insetRef.current.left / 2 : 0
    const centre = map.unproject(map.project(target, zoom).add([dx, dy]), zoom)
    map.flyTo(centre, zoom, { duration: 0.6 })
  }, [entity?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  return null
}


/** Screen area covered by floating panels, so the camera centres on what is visible. */
export interface MapInset {
  left: number
  bottom: number
}

/** Map centre that puts `target` in the middle of the part of the map nobody covers. */
function centreFor(map: L.Map, target: L.LatLng, zoom: number, inset: MapInset): L.LatLng {
  return map.unproject(map.project(target, zoom).add([-inset.left / 2, inset.bottom / 2]), zoom)
}

/**
 * Live camera for the selected vehicle. Picking a unit flies to it once; while
 * follow mode is on, every new position pans along. Dragging the map is the
 * person taking over, so it switches follow off (programmatic pans never fire
 * dragstart, so the camera cannot cancel itself).
 */
function FollowVehicle({
  vehicle,
  following,
  onFollowChange,
  inset,
}: {
  vehicle: Vehicle | undefined
  following: boolean
  onFollowChange: (v: boolean) => void
  inset: MapInset
}) {
  const map = useMap()
  const insetRef = useRef(inset)
  insetRef.current = inset
  const lastId = useRef<number | null>(null)

  useMapEvents({
    dragstart() {
      if (following) onFollowChange(false)
    },
  })

  const lat = vehicle && hasPosition(vehicle) ? vehicle.lat : null
  const lng = vehicle && hasPosition(vehicle) ? vehicle.lng : null
  const id = vehicle?.id ?? null

  useEffect(() => {
    if (id === null) {
      lastId.current = null
      return
    }
    if (lat === null || lng === null) return
    const target = L.latLng(lat, lng)
    const calm = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (lastId.current !== id) {
      // New selection: always bring the unit into view, follow or not.
      lastId.current = id
      const zoom = Math.max(map.getZoom(), 15)
      map.flyTo(centreFor(map, target, zoom, insetRef.current), zoom, { duration: calm ? 0 : 0.8 })
      return
    }
    if (following) {
      map.panTo(centreFor(map, target, map.getZoom(), insetRef.current), { animate: !calm, duration: 1.6, easeLinearity: 1 })
    }
  }, [id, lat, lng, following, map])

  // Turning follow back on re-centres straight away, even if the unit is parked.
  useEffect(() => {
    if (!following || lat === null || lng === null) return
    map.panTo(centreFor(map, L.latLng(lat, lng), map.getZoom(), insetRef.current), { animate: true, duration: 0.6 })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [following])

  return null
}

/** Fits every located vehicle whenever `signal` changes (the "fit fleet" button). */
function FitFleet({ vehicles, signal, inset }: { vehicles: Vehicle[]; signal: number; inset: MapInset }) {
  const map = useMap()
  const latest = useRef({ vehicles, inset })
  latest.current = { vehicles, inset }
  useEffect(() => {
    if (signal === 0) return
    const pts = latest.current.vehicles.filter(hasPosition).map((v) => [v.lat, v.lng] as [number, number])
    if (pts.length === 0) return
    const { inset: pad } = latest.current
    map.flyToBounds(L.latLngBounds(pts), {
      paddingTopLeft: [pad.left + 56, 72],
      paddingBottomRight: [56, pad.bottom + 56],
      maxZoom: 16,
      duration: 0.8,
    })
  }, [signal, map])
  return null
}

/**
 * Shows the route that was just planned. A line the camera does not look at
 * is a line the operator never sees, so the map flies to the whole path
 * whenever a new plan comes in.
 */
function FitRoute({
  focus,
  routes,
}: {
  focus: { id: number; n: number } | null
  routes: VehicleRoute[]
}) {
  const map = useMap()
  // The routes change on every position report; only the focus signal may
  // move the camera.
  const latest = useRef(routes)
  latest.current = routes

  useEffect(() => {
    if (!focus) return
    const route = latest.current.find((r) => r.id === focus.id)
    if (!route) return
    const pts = routeLatLngs(route)
    if (pts.length < 2) return
    const bounds = L.latLngBounds(pts)
    if (!bounds.isValid()) return
    map.flyToBounds(bounds.pad(0.3), { duration: 0.8, maxZoom: 15 })
  }, [focus?.n, map])

  return null
}

/**
 * Vehicle markers glide between positions with a CSS transition. During a zoom
 * or fly the map repositions every marker at once, and the glide would make
 * them lag behind, so it is switched off for the length of the move.
 */
function GlideGuard() {
  const map = useMap()
  useEffect(() => {
    const el = map.getContainer()
    let t: number | undefined
    const off = () => {
      window.clearTimeout(t)
      el.classList.add('no-glide')
    }
    const on = () => {
      window.clearTimeout(t)
      t = window.setTimeout(() => el.classList.remove('no-glide'), 160)
    }
    map.on('zoomstart', off)
    map.on('zoomend', on)
    return () => {
      window.clearTimeout(t)
      map.off('zoomstart', off)
      map.off('zoomend', on)
      el.classList.remove('no-glide')
    }
  }, [map])
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
  // Zones
  zones: ZoneFeature[]
  selectedZoneId: number | null
  onZoneSelect: (id: number | null) => void
  drawMode: boolean
  onZoneCreate: (geom: ZoneGeometry) => void
  onZoneEdit: (id: number, geom: ZoneGeometry) => void
  onZoneDelete: (id: number) => Promise<void>
  // Admin regions
  regionLevel: number
  regionParentCode: string | null
  regionData: RegionCollection | null
  regionVisible: boolean
  onRegionClick?: (region: RegionFeature) => void
  // Tracked vehicles (live)
  vehicles: Vehicle[]
  selectedVehicleId: number | null
  onVehicleSelect: (id: number | null) => void
  vehiclesVisible: boolean
  /** Whether this account may drive a given unit by dragging its marker. */
  canDrive: (v: Vehicle) => boolean
  onDrive: (id: number, lat: number, lng: number) => void
  following: boolean
  onFollowChange: (following: boolean) => void
  fitFleetSignal: number
  inset: MapInset
  // Delivery routes (from -> to)
  routes: VehicleRoute[]
  routesVisible: boolean
  routeDraft: { origin: RouteStop | null; destination: RouteStop | null } | null
  routePick: boolean
  onRoutePoint: (lat: number, lng: number) => void
  /** The route the camera has to show; the counter makes every plan move it. */
  focusRoute?: { id: number; n: number } | null
}

export default function EntityMap({
  entities,
  selected,
  onSelect,
  picking,
  draftCoords,
  onPick,
  theme,
  zones,
  selectedZoneId,
  onZoneSelect,
  drawMode,
  onZoneCreate,
  onZoneEdit,
  onZoneDelete,
  regionLevel,
  regionParentCode,
  regionData,
  regionVisible,
  onRegionClick,
  vehicles,
  selectedVehicleId,
  onVehicleSelect,
  vehiclesVisible,
  canDrive,
  onDrive,
  following,
  onFollowChange,
  fitFleetSignal,
  inset,
  routes,
  routesVisible,
  routeDraft,
  routePick,
  onRoutePoint,
  focusRoute,
}: Props) {
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
      className={picking || routePick ? 'map picking' : 'map'}
    >
      <TileLayer key={theme} attribution={MAP_ATTRIBUTION} url={TILE_URL} className="tiles-gmaps" maxZoom={19} />
      {/* Official administrative boundaries (read-only, underneath zones) */}
      <RegionLayer
        level={regionLevel}
        parentCode={regionParentCode}
        data={regionData}
        visible={regionVisible}
        interactive={!drawMode}
        onRegionClick={onRegionClick}
      />
      {/* Operator zones (editable polygons) */}
      <ZoneLayer
        zones={zones}
        selectedId={selectedZoneId}
        onSelect={onZoneSelect}
        drawMode={drawMode}
        onCreate={onZoneCreate}
        onEdit={onZoneEdit}
        onDelete={onZoneDelete}
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
      {/* Delivery routes sit under the vehicles that drive them */}
      <RouteLayer
        routes={routes}
        visible={routesVisible}
        draft={routeDraft}
        onSelectVehicle={onVehicleSelect}
      />
      {/* Live vehicles on top of every other layer */}
      <VehicleLayer
        vehicles={vehicles}
        selectedId={selectedVehicleId}
        onSelect={onVehicleSelect}
        visible={vehiclesVisible}
        canDrive={canDrive}
        onDrive={onDrive}
      />
      <FollowVehicle
        vehicle={vehicles.find((v) => v.id === selectedVehicleId)}
        following={following}
        onFollowChange={onFollowChange}
        inset={inset}
      />
      <InvalidateOnResize />
      <FitFleet vehicles={vehicles} signal={fitFleetSignal} inset={inset} />
      <FitRoute focus={focusRoute ?? null} routes={routes} />
      <GlideGuard />
      <ZoomControl position="topright" />
      <AttributionControl position="bottomright" prefix={false} />
      <FitAllControl entities={entities} />
      <ClickPicker picking={picking} onPick={onPick} routePick={routePick} onRoutePoint={onRoutePoint} />
      <FlyToSelected entity={selected} inset={inset} />
    </MapContainer>
  )
}
