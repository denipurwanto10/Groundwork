import { useEffect, useRef } from 'react'
import { useMap } from 'react-leaflet'
import L from 'leaflet'
import type { RegionFeature, RegionCollection, RegionProperties } from '../lib/types'

interface RegionLayerProps {
  /** Administrative level currently displayed (0 = country, 1 = province, ...). */
  level: number
  /** Only children of this parent code are shown (drill-down). */
  parentCode: string | null
  /** Pre-fetched collection. */
  data: RegionCollection | null
  /** Toggles the layer without refetching. */
  visible: boolean
  /**
   * When false the polygons stop swallowing pointer events, so clicks fall
   * through to the map (needed while drawing a zone on top of a boundary).
   */
  interactive?: boolean
  /** Called when the user clicks a boundary. */
  onRegionClick?: (region: RegionFeature) => void
}

function regionStyle(level: number, hovered: boolean): L.PathOptions {
  const base: L.PathOptions = {
    color: '#334155',
    weight: 1.2,
    fill: true,
    fillOpacity: 0.02,
    opacity: 0.75,
    className: 'region-polygon',
  }
  if (level === 0) {
    base.color = '#0f172a'
    base.weight = 2.4
    base.fillOpacity = 0
    base.opacity = 0.85
  } else if (level === 1) {
    base.color = '#1e293b'
    base.weight = 1.6
    base.fillOpacity = 0.03
  } else if (level === 2) {
    base.color = '#475569'
    base.weight = 1
    base.fillOpacity = 0.02
  } else {
    base.color = '#64748b'
    base.weight = 0.7
    base.fillOpacity = 0.015
  }
  if (hovered) {
    base.color = '#f97316'
    base.weight = (base.weight ?? 1) + 1
    base.opacity = 1
    base.fillOpacity = 0.08
  }
  return base
}

/**
 * Renders official administrative boundaries (admin_regions, imported from BPS
 * via HDX cod-ab-idn). Read-only: no editing, no Geoman. Drill-down is driven
 * by the parent panel, which fetches the next level.
 */
export default function RegionLayer({
  level,
  parentCode,
  data,
  visible,
  interactive = true,
  onRegionClick,
}: RegionLayerProps) {
  const map = useMap()
  const layerRef = useRef<L.GeoJSON | null>(null)
  const levelRef = useRef(level)
  const parentCodeRef = useRef(parentCode)
  const hoveredCodeRef = useRef<string | null>(null)
  const onClickRef = useRef(onRegionClick)
  const visibleRef = useRef(visible)
  levelRef.current = level
  parentCodeRef.current = parentCode
  onClickRef.current = onRegionClick
  visibleRef.current = visible

  // Create the layer once and keep it mounted; visibility is toggled below.
  // The layer is rebuilt when `interactive` flips because Leaflet reads
  // `interactive` from the path options when it is added to the map.
  useEffect(() => {
    const layer = L.geoJSON(null, {
      interactive,
      style: (feature) => {
        const props = feature?.properties as RegionProperties | undefined
        return regionStyle(levelRef.current, props?.code === hoveredCodeRef.current)
      },
      onEachFeature: (feature, lyr) => {
        const props = feature.properties as RegionProperties
        const path = lyr as L.Path
        const label = parentCodeRef.current
          ? `${props.name} (dalam ${parentCodeRef.current})`
          : props.name
        lyr.bindTooltip(label, { sticky: true, className: 'region-tip' })
        lyr.on('mouseover', () => {
          hoveredCodeRef.current = props.code
          path.setStyle(regionStyle(levelRef.current, true))
        })
        lyr.on('mouseout', () => {
          if (hoveredCodeRef.current === props.code) hoveredCodeRef.current = null
          path.setStyle(regionStyle(levelRef.current, false))
        })
        lyr.on('click', (e) => {
          e.originalEvent?.stopPropagation()
          onClickRef.current?.({
            type: 'Feature',
            properties: props,
            geometry: (feature.geometry ?? null) as RegionFeature['geometry'],
          })
        })
      },
    })
    if (visibleRef.current) layer.addTo(map)
    layerRef.current = layer
    return () => {
      layer.remove()
      layerRef.current = null
    }
  }, [map, interactive])

  // Sync data into the layer.
  useEffect(() => {
    const layer = layerRef.current
    if (!layer) return
    layer.clearLayers()
    if (!data) return
    for (const feature of data.features) {
      if (!feature.geometry) continue
      layer.addData({
        type: 'Feature',
        properties: feature.properties,
        geometry: feature.geometry,
      } as never)
    }
  }, [data, interactive])

  // Re-style when the level changes (styles depend on depth).
  useEffect(() => {
    const layer = layerRef.current
    if (!layer) return
    layer.eachLayer((lyr) => {
      const path = lyr as L.Path
      const props = (lyr as L.Layer & { feature?: { properties?: RegionProperties } }).feature
        ?.properties
      path.setStyle(regionStyle(level, props?.code === hoveredCodeRef.current))
    })
  }, [level, data, interactive])

  // Toggle visibility.
  useEffect(() => {
    const layer = layerRef.current
    if (!layer) return
    if (visible) {
      if (!map.hasLayer(layer)) layer.addTo(map)
    } else if (map.hasLayer(layer)) {
      map.removeLayer(layer)
    }
  }, [visible, map])

  return null
}
