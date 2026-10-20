import { useEffect, useRef, useState } from 'react'
import { useMap } from 'react-leaflet'
import L from 'leaflet'
import type { PM } from 'leaflet'
import '@geoman-io/leaflet-geoman-free'
import 'leaflet/dist/leaflet.css'
import '@geoman-io/leaflet-geoman-free/dist/leaflet-geoman.css'
import type { ZoneFeature, ZoneGeometry } from '../lib/types'

interface ZoneLayerProps {
  zones: ZoneFeature[]
  selectedId: number | null
  onSelect: (id: number | null) => void
  drawMode: boolean
  onCreate: (geom: ZoneGeometry) => void
  onEdit: (id: number, geom: ZoneGeometry) => void
  onDelete: (id: number) => Promise<void>
}

/** Geoman hands back a Leaflet layer; we only keep the GeoJSON geometry. */
function toZoneGeometry(layer: L.Layer): ZoneGeometry | null {
  const withGeo = layer as L.Layer & {
    toGeoJSON?: () => { geometry?: { type?: string; coordinates?: unknown } }
  }
  const geometry = withGeo.toGeoJSON?.()?.geometry
  if (!geometry) return null
  if (geometry.type !== 'Polygon' && geometry.type !== 'MultiPolygon') return null
  return { type: geometry.type, coordinates: geometry.coordinates }
}

function zoneStyle(zone: ZoneFeature | undefined, selected: boolean): L.PathOptions {
  const color = zone?.properties.color || '#2563eb'
  return {
    color,
    weight: selected ? 3 : 2,
    fillColor: color,
    fillOpacity: selected ? 0.28 : 0.12,
    opacity: selected ? 1 : 0.85,
    className: `zone-polygon${selected ? ' selected' : ''}`,
  }
}

function zoneToGeoJSON(zone: ZoneFeature) {
  return {
    type: 'Feature' as const,
    id: zone.id,
    properties: { zoneId: zone.id },
    geometry: zone.geometry,
  }
}

/**
 * Geoman adds a drag marker on every vertex, so enabling edit on an official
 * boundary (Bali & Nusa Tenggara: 227k points, 839 parts) locks the tab up.
 */
export const MAX_EDITABLE_VERTICES = 2000

export function vertexTotal(geometry: ZoneFeature['geometry'] | null | undefined): number {
  if (!geometry) return 0
  const polys = (geometry.type === 'Polygon' ? [geometry.coordinates] : geometry.coordinates) as number[][][][]
  return polys.reduce((sum, rings) => sum + rings.reduce((count, ring) => count + ring.length, 0), 0)
}

/**
 * Renders operator zones as editable polygons using Leaflet-Geoman.
 * When drawMode is true the Geoman toolbar appears: draw a polygon, drag
 * vertices, remove a polygon. Official administrative boundaries are read-only
 * and live in RegionLayer.
 */
export default function ZoneLayer({
  zones,
  selectedId,
  onSelect,
  drawMode,
  onCreate,
  onEdit,
  onDelete,
}: ZoneLayerProps) {
  const map = useMap()
  const geojsonLayerRef = useRef<L.GeoJSON | null>(null)
  // Bumped to force a re-sync when the map drifted from the zone list.
  const [syncTick, setSyncTick] = useState(0)

  // Latest values without re-creating the Leaflet layer on every render.
  const zonesRef = useRef(zones)
  const selectedRef = useRef(selectedId)
  const onSelectRef = useRef(onSelect)
  const onCreateRef = useRef(onCreate)
  const onEditRef = useRef(onEdit)
  const onDeleteRef = useRef(onDelete)
  zonesRef.current = zones
  selectedRef.current = selectedId
  onSelectRef.current = onSelect
  onCreateRef.current = onCreate
  onEditRef.current = onEdit
  onDeleteRef.current = onDelete

  // Create the layer once; data is synced in the effect below.
  useEffect(() => {
    const layer = L.geoJSON(null as never, {
      style: (feature) => {
        const id = Number(feature?.id)
        const zone = zonesRef.current.find((z) => z.id === id)
        return zoneStyle(zone, id === selectedRef.current)
      },
      onEachFeature: (feature, lyr) => {
        const zoneId = Number(feature.id)
        if (!Number.isFinite(zoneId)) return
        lyr.on('click', (e) => {
          e.originalEvent?.stopPropagation()
          onSelectRef.current(zoneId)
        })
        // Live edit: fires while a vertex is being dragged.
        lyr.on('pm:change', () => {
          const geometry = toZoneGeometry(lyr)
          if (geometry) onEditRef.current(zoneId, geometry)
        })
        // Commit: fires when Geoman finishes editing the shape.
        lyr.on('pm:update', () => {
          const geometry = toZoneGeometry(lyr)
          if (geometry) onEditRef.current(zoneId, geometry)
        })
      },
    })
    layer.addTo(map)
    geojsonLayerRef.current = layer
    return () => {
      layer.remove()
      geojsonLayerRef.current = null
    }
  }, [map])

  // Sync zone data into the layer.
  useEffect(() => {
    const layer = geojsonLayerRef.current
    if (!layer) return
    layer.clearLayers()
    for (const zone of zones) {
      if (!zone.geometry) continue
      layer.addData(zoneToGeoJSON(zone) as never)
    }
  }, [zones, syncTick])

  // Highlight the selected zone.
  useEffect(() => {
    const layer = geojsonLayerRef.current
    if (!layer) return
    layer.eachLayer((lyr) => {
      const path = lyr as L.Path
      const id = Number((lyr as L.Layer & { feature?: { id?: number } }).feature?.id)
      const zone = zonesRef.current.find((z) => z.id === id)
      path.setStyle(zoneStyle(zone, id === selectedRef.current))
    })
  }, [selectedId, zones])

  // Geoman toolbar: draw a polygon, edit vertices, remove a polygon.
  useEffect(() => {
    if (!drawMode) {
      map.pm.removeControls()
      return
    }
    map.pm.addControls({
      position: 'topright',
      drawMarker: false,
      drawCircleMarker: false,
      drawPolyline: false,
      drawRectangle: false,
      drawCircle: false,
      drawText: false,
      cutPolygon: false,
      dragMode: false,
      editMode: false,
      removalMode: true,
      rotateMode: false,
    })
    map.pm.setGlobalOptions({ allowSelfIntersection: false, snappable: true })

    const handleCreate: PM.CreateEventHandler = (e) => {
      const geometry = e.shape === 'Polygon' ? toZoneGeometry(e.layer) : null
      e.layer.remove()
      if (geometry) onCreateRef.current(geometry)
    }
    const handleRemove: PM.RemoveEventHandler = (e) => {
      const id = Number((e.layer as L.Layer & { feature?: { id?: number } }).feature?.id)
      if (!Number.isFinite(id)) return
      // Geoman already detached the shape from the map. Re-sync the whole layer
      // from the zone list so the map keeps matching state when the operator
      // cancels or the server refuses the delete.
      const restore = () => setSyncTick((tick) => tick + 1)
      if (!window.confirm('Delete this zone? This cannot be undone.')) {
        restore()
        return
      }
      onDeleteRef.current(id).catch(restore)
    }
    map.on('pm:create', handleCreate)
    map.on('pm:remove', handleRemove)
    return () => {
      map.off('pm:create', handleCreate)
      map.off('pm:remove', handleRemove)
      map.pm.removeControls()
    }
  }, [map, drawMode])

  // Turn edit mode on/off for every zone layer.
  useEffect(() => {
    const layer = geojsonLayerRef.current
    if (!layer) return
    layer.eachLayer((lyr) => {
      const path = lyr as L.Path
      if (!path.pm) return
      const feature = (lyr as L.Layer & { feature?: { geometry?: ZoneFeature['geometry'] } }).feature
      const editable = drawMode && vertexTotal(feature?.geometry) <= MAX_EDITABLE_VERTICES
      if (editable) {
        path.pm.enable({ allowSelfIntersection: false, allowSelfIntersectionEdit: false })
      } else {
        path.pm.disable()
      }
    })
  }, [drawMode, zones])

  return null
}
