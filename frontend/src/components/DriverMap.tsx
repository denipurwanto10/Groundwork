import { useEffect, useRef } from 'react'
import { MapContainer, TileLayer, useMap } from 'react-leaflet'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import type { Vehicle, VehicleRoute } from '../lib/types'
import { hasPosition } from '../lib/types'
import { MAP_ATTRIBUTION, TILE_SUBDOMAINS, TILE_URL } from '../lib/mapTiles'
import VehicleLayer from './VehicleLayer'
import RouteLayer from './RouteLayer'

/** Where the camera looks when the unit has never reported a position. */
const JAKARTA: [number, number] = [-6.2, 106.85]

/**
 * The camera of the driver map.
 *
 * Two rules, and they never fight:
 *
 * - A new trip (or a new unit) moves the camera once: the whole line is
 *   fitted into view, because the driver wants to see where the trip goes.
 * - While the unit drives, the camera only nudges: it pans the unit back
 *   into view when it would leave the screen, and leaves the rest alone.
 *
 * Dragging the map is the driver taking over, so the nudging stops (a
 * programmatic pan never fires dragstart, so the camera cannot cancel
 * itself).
 */
function Camera({ vehicle, route }: { vehicle: Vehicle | undefined; route: VehicleRoute | null }) {
  const map = useMap()
  const lastKey = useRef<string | null>(null)
  const following = useRef(true)

  useEffect(() => {
    const onDrag = () => {
      following.current = false
    }
    map.on('dragstart', onDrag)
    return () => {
      map.off('dragstart', onDrag)
    }
  }, [map])

  // The camera moves when the trip or the unit changes.
  const key = route ? `route-${route.id}` : vehicle ? `vehicle-${vehicle.id}` : null
  useEffect(() => {
    if (!key || key === lastKey.current) return
    lastKey.current = key
    following.current = true

    const calm = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (route) {
      const points = (route.geometry?.coordinates ?? [])
        .filter((pair) => Array.isArray(pair) && pair.length >= 2)
        .map((pair) => L.latLng(pair[1], pair[0]))
      if (points.length >= 2) {
        const bounds = L.latLngBounds(points)
        if (bounds.isValid()) {
          map.flyToBounds(bounds.pad(0.2), { duration: calm ? 0 : 0.8, maxZoom: 15 })
          return
        }
      }
    }
    if (vehicle && hasPosition(vehicle)) {
      const zoom = Math.max(map.getZoom(), 15)
      map.flyTo(L.latLng(vehicle.lat, vehicle.lng), zoom, { duration: calm ? 0 : 0.8 })
    }
  }, [key, route, vehicle, map])

  // The unit stays in sight while it drives, without yanking the camera.
  const lat = vehicle && hasPosition(vehicle) ? vehicle.lat : null
  const lng = vehicle && hasPosition(vehicle) ? vehicle.lng : null
  useEffect(() => {
    if (!following.current || lat === null || lng === null) return
    const calm = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    map.panInside(L.latLng(lat, lng), {
      paddingTopLeft: [48, 48],
      paddingBottomRight: [48, 48],
      animate: !calm,
      duration: 1.2,
    })
  }, [lat, lng, map])

  return null
}

interface Props {
  /** The unit of the driver, if there is one. */
  vehicle: Vehicle | undefined
  /** The route the unit is driving, if there is one. */
  route: VehicleRoute | null
  /** The theme, so the tiles follow the light/dark look of the app. */
  theme: 'light' | 'dark'
}

/**
 * The driver map: the unit, its route, and nothing else.
 *
 * It is a map of its own instead of the dashboard one, so the screen carries
 * only what a driver needs on the road: where the unit is and where it is
 * going.
 */
export default function DriverMap({ vehicle, route, theme }: Props) {
  const located = vehicle && hasPosition(vehicle) ? vehicle : undefined
  const center: [number, number] = located ? [located.lat, located.lng] : JAKARTA
  const colors = new Map<number, string>()
  if (vehicle) colors.set(vehicle.id, vehicle.route_color)

  return (
    <MapContainer
      center={center}
      zoom={located ? 15 : 12}
      zoomControl={false}
      attributionControl={false}
      className="map drv-map-canvas"
    >
      <TileLayer
        key={`${theme}-${TILE_URL}`}
        attribution={MAP_ATTRIBUTION}
        url={TILE_URL}
        subdomains={TILE_SUBDOMAINS}
        className="tiles-gmaps"
        maxZoom={20}
      />
      <RouteLayer
        routes={route ? [route] : []}
        visible
        draft={null}
        colors={colors}
        onSelectVehicle={() => {}}
      />
      <VehicleLayer
        vehicles={vehicle ? [vehicle] : []}
        selectedId={vehicle?.id ?? null}
        onSelect={() => {}}
        visible
        canDrive={() => false}
        onDrive={() => {}}
      />
      <Camera vehicle={vehicle} route={route} />
    </MapContainer>
  )
}
