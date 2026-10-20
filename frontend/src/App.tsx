import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './lib/api'
import type {
  Entity,
  EntityFieldErrors,
  EntityInput,
  ZoneFeature,
  ZoneGeometry,
  ZoneInput,
  ZoneCollection,
  ZoneImportReport,
  RegionFeature,
  RegionCollection,
  RouteStop,
  Vehicle,
  VehicleRoute,
} from './lib/types'
import EntityMap from './components/EntityMap'
import EntityList from './components/EntityList'
import EntityDetail from './components/EntityDetail'
import EntityForm from './components/EntityForm'
import ZonePanel from './components/ZonePanel'
import RegionZonePicker from './components/RegionZonePicker'
import VehiclePanel from './components/VehiclePanel'
import RegionControls, { type RegionPathEntry } from './components/RegionControls'
import ConfirmDialog from './components/ConfirmDialog'
import { openVehicleStream, type StreamState } from './lib/vehicleStream'
import {
  IconAlert,
  IconCheck,
  IconCrosshair,
  IconLayers,
  IconMoon,
  IconPanelLeft,
  IconPin,
  IconSun,
  IconPolygon,
  IconTruck,
} from './components/Icons'
import type { MapInset } from './components/EntityMap'
import './App.css'
import './modern.css'

type Toast = { kind: 'success' | 'error'; text: string } | null
type Theme = 'light' | 'dark'
type ApiState = 'checking' | 'online' | 'offline'

const API_STATE_UI: Record<ApiState, { label: string; title: string; cls: string }> = {
  checking: { label: 'API checking', title: 'Checking the backend connection', cls: 'checking' },
  online: { label: 'API online', title: 'Connected to the backend', cls: 'up' },
  offline: { label: 'API offline', title: 'The backend is not reachable', cls: 'down' },
}

const THEME_KEY = 'entity-manager:theme'

type Tab = 'entities' | 'zones' | 'vehicles'
type Sheet = 'closed' | 'half' | 'full'

/** Phone layout constants, mirrored in modern.css (--nav-h, --sheet-half, --sheet-full). */
const NAV_H = 64
const SHEET_FRACTION: Record<Sheet, number> = { closed: 0, half: 0.46, full: 0.84 }

const TABS: { key: Tab; label: string }[] = [
  { key: 'entities', label: 'Places' },
  { key: 'zones', label: 'Zones' },
  { key: 'vehicles', label: 'Live' },
]

/** True below the tablet breakpoint, where the sidebar becomes a bottom sheet. */
function useIsMobile(): boolean {
  const query = '(max-width: 767px)'
  const [mobile, setMobile] = useState(() => window.matchMedia(query).matches)
  useEffect(() => {
    const mq = window.matchMedia(query)
    const on = () => setMobile(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return mobile
}

function getInitialTheme(): Theme {
  try {
    const saved = localStorage.getItem(THEME_KEY)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    /* storage unavailable */
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export default function App() {
  const [entities, setEntities] = useState<Entity[]>([])
  const [visibleEntities, setVisibleEntities] = useState<Entity[]>([])
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [apiState, setApiState] = useState<ApiState>('checking')
  const [toast, setToast] = useState<Toast>(null)
  const [theme, setTheme] = useState<Theme>(getInitialTheme)

  // Zone state
  const [zones, setZones] = useState<ZoneFeature[]>([])
  const [zonesLoading, setZonesLoading] = useState(true)
  const [zonesError, setZonesError] = useState<string | null>(null)
  const [selectedZoneId, setSelectedZoneId] = useState<number | null>(null)
  const [drawMode, setDrawMode] = useState(false)
  /** Working geometry: drawn on the map, or produced by dragging zone vertices. */
  const [zoneDraftGeometry, setZoneDraftGeometry] = useState<ZoneGeometry | null>(null)
  /** Zone form is open (new or edit); editingId is null while creating. */
  const [zoneFormOpen, setZoneFormOpen] = useState(false)
  const [zoneEditingId, setZoneEditingId] = useState<number | null>(null)

  // Admin region drill-down state
  const [regionLevel, setRegionLevel] = useState(1) // start at province level
  const [regionParentCode, setRegionParentCode] = useState<string | null>(null)
  /** Ancestors walked down from the country level, for the breadcrumb. */
  const [regionPath, setRegionPath] = useState<RegionPathEntry[]>([])
  const [regionData, setRegionData] = useState<RegionCollection | null>(null)
  const [regionsVisible, setRegionsVisible] = useState(true)
  const [regionsLoading, setRegionsLoading] = useState(false)
  const [regionNote, setRegionNote] = useState<string | null>(null)

  // Live vehicle tracking: REST gives the first snapshot, the stream keeps it
  // current. selectedVehicleId drives the panel/marker highlight only.
  const [vehicles, setVehicles] = useState<Vehicle[]>([])
  const [vehiclesLoading, setVehiclesLoading] = useState(true)
  const [vehiclesError, setVehiclesError] = useState<string | null>(null)
  const [vehiclesVisible, setVehiclesVisible] = useState(true)
  const [selectedVehicleId, setSelectedVehicleId] = useState<number | null>(null)
  const [streamState, setStreamState] = useState<StreamState>('connecting')

  // Delivery routes: one active path per vehicle, drawn under the markers.
  // The draft holds the two stops picked on the map until they are saved.
  const [routes, setRoutes] = useState<VehicleRoute[]>([])
  const [routeDraft, setRouteDraft] = useState<{ origin: RouteStop | null; destination: RouteStop | null }>({
    origin: null,
    destination: null,
  })
  const [routePick, setRoutePick] = useState<'origin' | 'destination' | null>(null)

  // Sidebar tab
  const [sidebarTab, setSidebarTab] = useState<Tab>('entities')
  // Phone: height of the bottom sheet. Desktop: the sidebar can be tucked away.
  const [sheet, setSheet] = useState<Sheet>('half')
  const [collapsed, setCollapsed] = useState(false)
  const isMobile = useIsMobile()
  const [viewport, setViewport] = useState(0)
  useEffect(() => {
    const on = () => setViewport((n) => n + 1)
    window.addEventListener('resize', on)
    return () => window.removeEventListener('resize', on)
  }, [])

  // Live tracking camera: follow the selected unit, and a counter the map
  // watches to fit the whole fleet into view.
  const [following, setFollowing] = useState(false)
  const [fitFleetSignal, setFitFleetSignal] = useState(0)
  const grabY = useRef<number | null>(null)

    // Entity form state
    const [formOpen, setFormOpen] = useState(false)
    const [formMode, setFormMode] = useState<'create' | 'edit'>('create')
    const [saving, setSaving] = useState(false)
    const [serverFields, setServerFields] = useState<EntityFieldErrors>({})

    const [confirmOpen, setConfirmOpen] = useState(false)
    const [deleting, setDeleting] = useState(false)

    const [picking, setPicking] = useState(false)
    const [draftCoords, setDraftCoords] = useState<{ latitude: number; longitude: number } | null>(null)

    const listSlotRef = useRef<HTMLDivElement>(null)

  // Theme: applied before paint, persisted only when the person chooses one.
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
  }, [theme])

  const toggleTheme = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark'
    setTheme(next)
    try {
      localStorage.setItem(THEME_KEY, next)
    } catch {
      /* storage unavailable */
    }
  }

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const showToast = useCallback((kind: 'success' | 'error', text: string) => {
    setToast({ kind, text })
  }, [])

  // A selected work area narrows the entity list and the map pins to it.
  const refresh = useCallback(async () => {
    // Offline polls call refresh too — only the initial mount shows "checking".
    setLoading(true)
    setLoadError(null)
    try {
      const data = await api.listEntities(selectedZoneId)
      setEntities(data)
      setApiState('online')
      setSelectedId((prev) => (prev != null && !data.some((e) => e.id === prev) ? null : prev))
    } catch (err) {
      // Gagal memuat daftar = backend bermasalah (mati, 5xx, dsb),
      // apa pun jenis error-nya — badge harus ikut offline.
      setApiState('offline')
      setLoadError(err instanceof Error ? err.message : 'Could not load entities.')
    } finally {
      setLoading(false)
    }
  }, [selectedZoneId])

    // Zone refresh
    const refreshZones = useCallback(async () => {
      setZonesLoading(true)
      setZonesError(null)
      try {
        const data = await api.listZones()
        setZones(data.features)
      } catch (err) {
        setZonesError(err instanceof Error ? err.message : 'Could not load zones')
      } finally {
        setZonesLoading(false)
      }
    }, [])

    // Region fetch for drill-down
    const loadRegions = useCallback(
      async (level: number, parentCode: string | null): Promise<RegionCollection | null> => {
        setRegionsLoading(true)
        setRegionNote(null)
        try {
          const data = await api.listRegions({ level, parent: parentCode ?? undefined, geom: true })
          setRegionData(data)
          if (data.features.length === 0) {
            setRegionNote(
              parentCode
                ? 'No boundaries stored for this area yet.'
                : 'No boundaries stored for this level yet.',
            )
          }
          return data
        } catch (err) {
          setRegionData(null)
          setRegionNote(err instanceof Error ? err.message : 'Could not load boundaries.')
          return null
        } finally {
          setRegionsLoading(false)
        }
      },
      [],
    )

    // Load zones on mount (after entities)
    useEffect(() => {
      refreshZones()
    }, [refreshZones])

    // Load the province boundaries on mount.
    useEffect(() => {
      void loadRegions(1, null)
    }, [loadRegions])

    // Fleet snapshot: the REST list is the starting state, then the stream keeps
    // it current. Runs on mount and whenever the operator asks for a reload.
    const refreshVehicles = useCallback(async () => {
      setVehiclesLoading(true)
      setVehiclesError(null)
      try {
        const data = await api.listVehicles()
        setVehicles(data.vehicles)
      } catch (err) {
        setVehiclesError(err instanceof Error ? err.message : 'Could not load vehicles')
      } finally {
        setVehiclesLoading(false)
      }
    }, [])

    useEffect(() => {
      void refreshVehicles()
    }, [refreshVehicles])

    // Routes are a separate list: the map draws them for the whole fleet, not
    // only for the selected unit.
    const refreshRoutes = useCallback(async () => {
      try {
        const data = await api.listRoutes()
        setRoutes(data.routes)
      } catch {
        // A missing route list must not break the fleet view; the map simply
        // shows no paths until the next attempt.
        setRoutes([])
      }
    }, [])

    useEffect(() => {
      void refreshRoutes()
    }, [refreshRoutes])

    // Live positions. openVehicleStream reconnects on its own; a snapshot
    // replaces the list (it is authoritative), a position upserts one unit,
    // and a route frame keeps the paths in step without a reload.
    useEffect(
      () =>
        openVehicleStream({
          onFrame: (frame) => {
            if (frame.type === 'snapshot') {
              setVehicles(frame.vehicles)
              setVehiclesError(null)
              setVehiclesLoading(false)
              return
            }
            if (frame.type === 'route') {
              setRoutes((prev) => {
                const next = prev.filter((r) => !(r.vehicle_id === frame.route.vehicle_id && r.status === 'active'))
                return [frame.route, ...next]
              })
              return
            }
            if (frame.type === 'route_cleared') {
              setRoutes((prev) => prev.filter((r) => r.id !== frame.route_id))
              return
            }
            if (frame.type === 'route_completed') {
              // The line leaving the map is quiet, so say it out loud: the unit
              // reached its destination and the server closed the delivery.
              setRoutes((prev) => prev.filter((r) => r.id !== frame.route_id))
              setToast({ kind: 'success', text: 'Delivery complete — route closed.' })
              return
            }
            const incoming = frame.vehicle
            setVehicles((prev) => {
              const at = prev.findIndex((v) => v.id === incoming.id)
              if (at === -1) return [...prev, incoming]
              const next = prev.slice()
              next[at] = incoming
              return next
            })
          },
          onState: setStreamState,
        }),
      [],
    )

    // Drop the selection when a unit leaves the fleet.
    useEffect(() => {
      if (selectedVehicleId == null) return
      if (!vehicles.some((v) => v.id === selectedVehicleId)) setSelectedVehicleId(null)
    }, [vehicles, selectedVehicleId])

    // Half-picked stops belong to one vehicle: switching units or closing the
    // picker must not leave a stray A/B marker on the map.
    useEffect(() => {
      setRouteDraft({ origin: null, destination: null })
      setRoutePick(null)
    }, [selectedVehicleId])

    // Picking a unit (list or map) opens the live view for it and starts following.
    const selectVehicle = useCallback((id: number | null) => {
      setSelectedVehicleId(id)
      if (id == null) return
      setFollowing(true)
      setSelectedId(null)
      setSidebarTab('vehicles')
      setSheet((s) => (s === 'closed' ? 'half' : s))
    }, [])

    const handleRoutePickStart = (stage: 'origin' | 'destination') => {
      // One map mode at a time: arming a route stop disarms entity pinning.
      setPicking(false)
      setRoutePick(stage)
    }

    const handleRoutePoint = (lat: number, lng: number) => {
      if (!routePick) return
      const stop: RouteStop = {
        lat: Number(lat.toFixed(6)),
        lng: Number(lng.toFixed(6)),
        name: '',
      }
      setRouteDraft((prev) => ({ ...prev, [routePick]: stop }))
      setRoutePick(null)
    }

    const handleRouteLabel = (stage: 'origin' | 'destination', name: string) => {
      setRouteDraft((prev) => {
        const current = prev[stage]
        if (!current) return prev
        return { ...prev, [stage]: { ...current, name } }
      })
    }

    const handleRouteSet = async (vehicleId: number, origin: RouteStop, destination: RouteStop) => {
      const route = await api.setVehicleRoute(vehicleId, { origin, destination })
      setRoutes((prev) => [route, ...prev.filter((r) => !(r.vehicle_id === vehicleId && r.status === 'active'))])
      setRouteDraft({ origin: null, destination: null })
      setRoutePick(null)
      showToast('success', `Route planned: ${Math.round(route.distance_m / 100) / 10} km`)
    }

    const handleRouteClear = async (vehicleId: number) => {
      await api.clearVehicleRoute(vehicleId)
      setRoutes((prev) => prev.filter((r) => r.vehicle_id !== vehicleId))
      setRouteDraft({ origin: null, destination: null })
      showToast('success', 'Route cleared')
    }

    // A zone built from a boundary arrives complete: refresh the list and open
    // it, so the operator sees exactly what was copied.
    const handleZoneCreated = useCallback(
      (zone: ZoneFeature) => {
        setZones((prev) => [...prev.filter((z) => z.properties.id !== zone.properties.id), zone])
        setSelectedZoneId(zone.properties.id)
        setSidebarTab('zones')
        showToast('success', `Zone "${zone.properties.name}" created from the boundary`)
      },
      [showToast],
    )

    // Entities load on mount; the offline poll and the actions refresh them after.
    useEffect(() => {
      void refresh()
    }, [refresh])

    // Auto-retry while offline; stops as soon as a poll succeeds.
    useEffect(() => {
      if (apiState !== 'offline') return
      const id = setInterval(() => void refresh(), 15000)
      return () => clearInterval(id)
    }, [apiState, refresh])

  const selected = entities.find((e) => e.id === selectedId)

  const fleet = useMemo(() => {
    const c = { moving: 0, idle: 0, offline: 0 }
    for (const v of vehicles) {
      const st = v.status === 'moving' || v.status === 'idle' ? v.status : 'offline'
      c[st] += 1
    }
    return c
  }, [vehicles])

  // The map shows filtered pins, but the open detail's pin must stay visible
  // even when the current filter would hide it.
  const mapEntities = useMemo(() => {
    if (selected && !visibleEntities.some((e) => e.id === selected.id)) {
      return [...visibleEntities, selected]
    }
    return visibleEntities
  }, [visibleEntities, selected])

  // While the form is docked over the list, the list underneath must not be reachable by keyboard.
  useEffect(() => {
    listSlotRef.current?.toggleAttribute('inert', formOpen)
  }, [formOpen])

  const openCreate = () => {
    setFormMode('create')
    setServerFields({})
    setDraftCoords(null)
    setPicking(true)
    setFormOpen(true)
  }

  const openEdit = () => {
    if (!selected) return
    setFormMode('edit')
    setServerFields({})
    setDraftCoords({ latitude: selected.latitude, longitude: selected.longitude })
    setPicking(true)
    setFormOpen(true)
  }

  const closeForm = () => {
    setFormOpen(false)
    setPicking(false)
    setDraftCoords(null)
    setServerFields({})
  }

  // Escape closes the top-most layer: dialog → form → details.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      if (confirmOpen) {
        if (!deleting) setConfirmOpen(false)
      } else if (formOpen) {
        if (!saving) closeForm()
      } else if (selectedId != null) {
        setSelectedId(null)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [confirmOpen, deleting, formOpen, saving, selectedId])

  const handleSubmit = async (input: EntityInput) => {
    setSaving(true)
    setServerFields({})
    try {
      if (formMode === 'create') {
        const created = await api.createEntity(input)
        await refresh()
        setSelectedId(created.id)
        showToast('success', `${created.name} added`)
      } else if (selected) {
        const updated = await api.updateEntity(selected.id, input)
        await refresh()
        setSelectedId(updated.id)
        showToast('success', `${updated.name} saved`)
      }
      closeForm()
    } catch (err) {
      if (err instanceof ApiError && err.code === 'validation_failed' && err.fields) {
        setServerFields(err.fields as EntityFieldErrors)
      } else {
        showToast('error', err instanceof Error ? err.message : 'Could not save.')
      }
    } finally {
          setSaving(false)
        }
      }

      // Zone callbacks
      const handleZoneCreate = useCallback(async (input: ZoneInput) => {
        const created = await api.createZone(input)
        showToast('success', `Zone "${created.properties.name}" created`)
        await refreshZones()
      }, [refreshZones])

      const handleZoneUpdate = useCallback(async (id: number, input: ZoneInput) => {
        const updated = await api.updateZone(id, input)
        showToast('success', `Zone "${updated.properties.name}" updated`)
        await refreshZones()
      }, [refreshZones])

      const handleZoneDelete = useCallback(async (id: number) => {
        try {
          await api.deleteZone(id)
          showToast('success', 'Zone deleted')
          // Deleting the area you were working in must not leave the map filtered.
          if (selectedZoneId === id) setSelectedZoneId(null)
          await refreshZones()
        } catch (err) {
          showToast('error', err instanceof Error ? err.message : 'Could not delete zone')
          // Re-read the server state so the map matches the database again.
          await refreshZones()
          throw err
        }
      }, [refreshZones, selectedZoneId])

      // A polygon drawn on the map becomes the working geometry of the form.
      const handleZoneGeometryCreated = useCallback((geom: ZoneGeometry) => {
        setZoneDraftGeometry(geom)
        setZoneEditingId(null)
        setZoneFormOpen(true)
      }, [])

      // Dragging a vertex of an existing zone saves the new shape, debounced so
      // a drag produces one request instead of one per pointer move.
      const zoneEditTimer = useRef<number | null>(null)
      const zoneEditPending = useRef<{ id: number; geom: ZoneGeometry } | null>(null)

      const handleZoneGeometryEdited = useCallback(
        (id: number, geom: ZoneGeometry) => {
          zoneEditPending.current = { id, geom }
          if (zoneEditTimer.current !== null) window.clearTimeout(zoneEditTimer.current)
          zoneEditTimer.current = window.setTimeout(() => {
            const pending = zoneEditPending.current
            zoneEditPending.current = null
            zoneEditTimer.current = null
            if (!pending) return
            const zone = zones.find((z) => z.id === pending.id)
            if (!zone) return
            const p = zone.properties
            api
              .updateZone(pending.id, {
                name: p.name,
                category: p.category,
                color: p.color,
                description: p.description,
                region_level: p.region_level,
                region_code: p.region_code,
                geometry: pending.geom,
              })
              .then(() => {
                showToast('success', 'Zone shape saved')
                void refreshZones()
              })
              .catch((err) =>
                showToast('error', err instanceof Error ? err.message : 'Could not save the shape.'),
              )
          }, 700)
        },
        [zones, refreshZones, showToast],
      )

      useEffect(
        () => () => {
          if (zoneEditTimer.current !== null) window.clearTimeout(zoneEditTimer.current)
        },
        [],
      )

      const handleZoneImport = useCallback(async (file: File): Promise<ZoneImportReport | null> => {
        try {
          const text = await file.text()
          const collection = JSON.parse(text) as ZoneCollection
          if (collection.type !== 'FeatureCollection' || !Array.isArray(collection.features)) {
            throw new Error('Invalid GeoJSON: must be a FeatureCollection')
          }
          const report = await api.importZones(collection)
          showToast('success', `Imported ${report.imported} zone(s)${report.failed.length ? `, ${report.failed.length} failed` : ''}`)
          await refreshZones()
          return report
        } catch (err) {
          const msg = err instanceof Error ? err.message : 'Import failed'
          showToast('error', msg)
          return null
        }
      }, [refreshZones])

      const handleZoneExport = useCallback(async () => {
        const collection = await api.listZones()
        const blob = new Blob([JSON.stringify(collection, null, 2)], { type: 'application/geo+json' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = `zones-${new Date().toISOString().slice(0, 10)}.geojson`
        a.click()
        URL.revokeObjectURL(url)
      }, [])

      // Region click -> drill down one administrative level
      const handleRegionClick = useCallback(
        (region: RegionFeature) => {
          const nextLevel = region.properties.level + 1
          if (nextLevel > 4) return
          setRegionLevel(nextLevel)
          setRegionParentCode(region.properties.code)
          setRegionPath((prev) => [
            ...prev,
            { code: region.properties.code, name: region.properties.name },
          ])
          void loadRegions(nextLevel, region.properties.code)
        },
        [loadRegions],
      )

      // Breadcrumb navigation: -1 is the country root, otherwise an index into
      // the current path.
      const handleRegionJump = useCallback(
        (crumbIndex: number) => {
          const nextPath = crumbIndex < 0 ? [] : regionPath.slice(0, crumbIndex + 1)
          const parent = nextPath.length ? nextPath[nextPath.length - 1].code : null
          const level = nextPath.length + 1
          setRegionLevel(level)
          setRegionParentCode(parent)
          setRegionPath(nextPath)
          void loadRegions(level, parent)
        },
        [regionPath, loadRegions],
      )

      const handleRegionRefresh = useCallback(() => {
        void loadRegions(regionLevel, regionParentCode)
      }, [loadRegions, regionLevel, regionParentCode])

  const handleDelete = async () => {
    if (!selected) return
    setDeleting(true)
    try {
      await api.deleteEntity(selected.id)
      showToast('success', `${selected.name} deleted`)
      setConfirmOpen(false)
      setSelectedId(null)
      await refresh()
    } catch (err) {
      showToast('error', err instanceof Error ? err.message : 'Could not delete.')
    } finally {
      setDeleting(false)
    }
  }

  // On phones the list sheet gives way to the detail sheet / form sheet.
  const covered = selected != null || formOpen

  // The part of the map hidden behind panels, so the camera centres on what is
  // actually visible: the sidebar on desktop, the sheet + nav bar on a phone.
  const mapInset = useMemo<MapInset>(() => {
    void viewport
    if (isMobile) {
      const sheetPx = sheet === 'closed' || covered ? 0 : SHEET_FRACTION[sheet] * window.innerHeight
      return { left: 0, bottom: NAV_H + sheetPx }
    }
    if (collapsed) return { left: 0, bottom: 0 }
    const w = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--sidebar-w')) || 368
    return { left: w + 32, bottom: 0 }
  }, [isMobile, sheet, covered, collapsed, viewport])

  const handleTab = (tab: Tab) => {
    if (tab === sidebarTab) {
      if (isMobile) setSheet((s) => (s === 'closed' ? 'half' : 'closed'))
      return
    }
    setSidebarTab(tab)
    if (isMobile) setSheet((s) => (s === 'closed' ? 'half' : s))
  }

  const cycleSheet = (dir: 1 | -1 | 0) => {
    const order: Sheet[] = ['closed', 'half', 'full']
    setSheet((s) => {
      if (dir === 0) return s === 'half' ? 'full' : 'half'
      return order[Math.min(2, Math.max(0, order.indexOf(s) + dir))]
    })
  }

  return (
      <div
        className={`app${collapsed ? ' is-collapsed' : ''}${sheet !== 'closed' ? ' sheet-open' : ''}`}
        data-tab={sidebarTab}
        data-sheet={sheet}
      >
        <div className="map-stage">
          <EntityMap
            entities={mapEntities}
            selected={selected}
            onSelect={setSelectedId}
            picking={picking && formOpen}
            draftCoords={draftCoords}
            onPick={(lat, lng) => setDraftCoords({ latitude: lat, longitude: lng })}
            theme={theme}
            // Zones
            zones={zones}
            selectedZoneId={selectedZoneId}
            onZoneSelect={setSelectedZoneId}
            drawMode={drawMode}
            onZoneCreate={handleZoneGeometryCreated}
            onZoneEdit={handleZoneGeometryEdited}
            onZoneDelete={handleZoneDelete}
            // Admin regions
            regionLevel={regionLevel}
            regionParentCode={regionParentCode}
            regionData={regionData}
            regionVisible={regionsVisible}
            onRegionClick={handleRegionClick}
            // Live vehicles
            vehicles={vehicles}
            selectedVehicleId={selectedVehicleId}
            onVehicleSelect={selectVehicle}
            vehiclesVisible={vehiclesVisible}
            following={following}
            onFollowChange={setFollowing}
            fitFleetSignal={fitFleetSignal}
            inset={mapInset}
            // Delivery routes (from -> to)
            routes={routes}
            routesVisible={vehiclesVisible}
            routeDraft={routeDraft}
            routePick={routePick !== null}
            onRoutePoint={handleRoutePoint}
          />
          <RegionControls
            level={regionLevel}
            path={regionPath}
            count={regionData?.features.length ?? 0}
            loading={regionsLoading}
            visible={regionsVisible}
            note={regionNote}
            onVisibleChange={setRegionsVisible}
            onJumpTo={handleRegionJump}
            onRefresh={handleRegionRefresh}
          />

          {collapsed && (
            <button
              type="button"
              className="sidebar-reopen"
              onClick={() => setCollapsed(false)}
              aria-label="Show the side panel"
              title="Show the side panel"
            >
              <IconPanelLeft width={18} height={18} />
              <span>Panel</span>
            </button>
          )}

          {sidebarTab === 'vehicles' && vehiclesVisible && vehicles.length > 0 && (
            <div className="live-hud" role="status" aria-label="Fleet status">
              <span className={`hud-live ${streamState}`}>
                <i aria-hidden="true" />
                {streamState === 'live' ? 'Live' : streamState === 'connecting' ? 'Connecting' : 'Reconnecting'}
              </span>
              <span className="hud-n moving">
                <b>{fleet.moving}</b> moving
              </span>
              <span className="hud-n idle">
                <b>{fleet.idle}</b> idle
              </span>
              <span className="hud-n offline">
                <b>{fleet.offline}</b> offline
              </span>
            </div>
          )}
        </div>

        <aside className={covered ? 'sidebar is-covered' : 'sidebar'} aria-hidden={!isMobile && collapsed ? true : undefined}>
          <header className="brandbar">
            <div className="brand">
              <span className="brand-mark">
                <IconLayers width={20} height={20} />
              </span>
              <div className="brand-text">
                <h1>Groundwork</h1>
                <span className="brand-sub">Field operations, mapped</span>
              </div>
            </div>
            <div className="brandbar-tools">
              <div className={`backend-status ${API_STATE_UI[apiState].cls}`} role="status" title={API_STATE_UI[apiState].title}>
                <span className={`dot ${API_STATE_UI[apiState].cls}`} aria-hidden="true" />
                <span className="status-label">{API_STATE_UI[apiState].label}</span>
              </div>
              <button
                className="icon-btn"
                onClick={toggleTheme}
                aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
                title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
              >
                {theme === 'dark' ? <IconSun /> : <IconMoon />}
              </button>
              <button
                className="icon-btn collapse-btn"
                onClick={() => setCollapsed(true)}
                aria-label="Hide the side panel"
                title="Hide the side panel"
              >
                <IconPanelLeft />
              </button>
            </div>
          </header>

          {/* Desktop: segmented tabs under the header. Phone: bottom navigation bar. */}
          <nav className="sidebar-tabs" role="tablist" aria-label="Workspace">
            {TABS.map((t) => {
              const active = sidebarTab === t.key
              const count = t.key === 'entities' ? entities.length : t.key === 'zones' ? zones.length : vehicles.length
              const Icon = t.key === 'entities' ? IconPin : t.key === 'zones' ? IconPolygon : IconTruck
              return (
                <button
                  key={t.key}
                  role="tab"
                  aria-selected={active}
                  onClick={() => handleTab(t.key)}
                  className={active ? 'active' : ''}
                >
                  <Icon width={20} height={20} />
                  <span className="tab-label">{t.label}</span>
                  <span className={`tab-badge${t.key === 'vehicles' && streamState === 'live' ? ' live' : ''}`}>{count}</span>
                </button>
              )
            })}
          </nav>

          <div
            className="list-slot"
            ref={listSlotRef}
            data-state={sheet}
            onFocusCapture={(e) => {
              // The keyboard covers half a phone: give a focused field the whole sheet.
              if (isMobile && (e.target as HTMLElement).matches('input, textarea, select')) setSheet('full')
            }}
          >
            <button
              type="button"
              className="sheet-grab"
              aria-label={sheet === 'full' ? 'Lower the panel' : 'Raise the panel'}
              onPointerDown={(e) => {
                grabY.current = e.clientY
                e.currentTarget.setPointerCapture?.(e.pointerId)
              }}
              onPointerCancel={() => {
                grabY.current = null
              }}
              onPointerUp={(e) => {
                const dy = e.clientY - (grabY.current ?? e.clientY)
                grabY.current = null
                if (dy < -28) cycleSheet(1)
                else if (dy > 28) cycleSheet(-1)
                else cycleSheet(0)
              }}
              onClick={(e) => {
                // Pointer taps are handled above; this only fires for keyboard activation.
                if (e.detail === 0) cycleSheet(0)
              }}
            >
              <span aria-hidden="true" />
            </button>
            {sidebarTab === 'entities' ? (
              <EntityList
                entities={entities}
                selectedId={selectedId}
                loading={loading}
                error={loadError}
                onSelect={setSelectedId}
                onAdd={openCreate}
                onRetry={refresh}
                onVisibleEntitiesChange={setVisibleEntities}
                scopeName={
                  selectedZoneId != null
                    ? (zones.find((z) => z.properties.id === selectedZoneId)?.properties.name ?? null)
                    : null
                }
                onClearScope={() => setSelectedZoneId(null)}
              />
            ) : sidebarTab === 'zones' ? (
              <>
                <RegionZonePicker onCreated={handleZoneCreated} />
                <ZonePanel
                zones={zones}
                selectedId={selectedZoneId}
                loading={zonesLoading}
                error={zonesError}
                drawMode={drawMode}
                onDrawModeChange={setDrawMode}
                onSelect={setSelectedZoneId}
                onCreate={handleZoneCreate}
                onUpdate={handleZoneUpdate}
                onDelete={handleZoneDelete}
                onImport={handleZoneImport}
                onExport={handleZoneExport}
                formOpen={zoneFormOpen}
                onFormOpenChange={setZoneFormOpen}
                editingId={zoneEditingId}
                onEditingIdChange={setZoneEditingId}
                geometry={zoneDraftGeometry}
                onGeometryChange={setZoneDraftGeometry}
                regions={regionData?.features ?? []}
                regionLevel={regionLevel}
                />
              </>
            ) : (
              <VehiclePanel
                vehicles={vehicles}
                selectedId={selectedVehicleId}
                loading={vehiclesLoading}
                error={vehiclesError}
                streamState={streamState}
                visible={vehiclesVisible}
                onVisibleChange={setVehiclesVisible}
                onSelect={selectVehicle}
                onRetry={refreshVehicles}
                following={following}
                onFollowChange={setFollowing}
                onFitFleet={() => {
                  setFollowing(false)
                  setFitFleetSignal((n) => n + 1)
                }}
                routes={routes}
                draft={routeDraft}
                picking={routePick}
                onPickStart={handleRoutePickStart}
                onPickCancel={() => setRoutePick(null)}
                onLabelChange={handleRouteLabel}
                onClearDraft={() => {
                  setRouteDraft({ origin: null, destination: null })
                  setRoutePick(null)
                }}
                onRouteSet={handleRouteSet}
                onRouteClear={handleRouteClear}
              />
            )}
          </div>
        </aside>

      {!formOpen && (
        <EntityDetail
          entity={selected}
          deleting={deleting}
          onEdit={openEdit}
          onDelete={() => setConfirmOpen(true)}
          onClose={() => setSelectedId(null)}
        />
      )}

      {routePick !== null && (
        <div className="route-pick-hint" role="status">
          <IconCrosshair width={16} height={16} />
          <span>Click the map to set {routePick === 'origin' ? 'the origin (A)' : 'the destination (B)'}</span>
          <button type="button" onClick={() => setRoutePick(null)}>
            Cancel
          </button>
        </div>
      )}

      <EntityForm
        open={formOpen}
        mode={formMode}
        initial={formMode === 'edit' ? (selected ?? null) : null}
        draftCoords={draftCoords}
        saving={saving}
        picking={picking}
        serverFields={serverFields}
        onClose={closeForm}
        onSubmit={handleSubmit}
        onCoordsChange={() => setServerFields({})}
        onTogglePick={() => setPicking((p) => !p)}
      />

      <ConfirmDialog
        open={confirmOpen}
        title="Delete this entity?"
        message={selected ? `"${selected.name}" will be removed for good.` : ''}
        confirmLabel="Delete"
        busy={deleting}
        onConfirm={handleDelete}
        onCancel={() => setConfirmOpen(false)}
      />

      {toast && (
        <div className={`toast ${toast.kind}`} role={toast.kind === 'error' ? 'alert' : 'status'}>
          {toast.kind === 'error' ? <IconAlert width={18} height={18} /> : <IconCheck width={18} height={18} />}
          <span>{toast.text}</span>
        </div>
      )}
    </div>
  )
}
