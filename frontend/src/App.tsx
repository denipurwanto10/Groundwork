import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './lib/api'
import type {
  Entity,
  EntityFieldErrors,
  EntityInput,
  GeofenceEvent,
  ZoneCount,
  ZoneFeature,
  ZoneGeometry,
  ZoneInput,
  ZoneCollection,
  ZoneImportReport,
  RegionFeature,
  RegionCollection,
  RouteStop,
  UserAccount,
  UserInput,
  Vehicle,
  VehicleInput,
  VehicleRoute,
} from './lib/types'
import { activeRoutesOf, bearingBetween, formatDistance, hasPosition, mergeRoutes } from './lib/types'
import EntityMap from './components/EntityMap'
import EntityList from './components/EntityList'
import EntityDetail from './components/EntityDetail'
import EntityForm from './components/EntityForm'
import ZonePanel from './components/ZonePanel'
import RegionZonePicker from './components/RegionZonePicker'
import VehiclePanel from './components/VehiclePanel'
import UserPanel from './components/UserPanel'
import RegionControls, { type RegionPathEntry } from './components/RegionControls'
import ConfirmDialog from './components/ConfirmDialog'
import { openVehicleStream, type StreamState } from './lib/vehicleStream'
import { usePlayback } from './lib/playback'
import { KpiGrid, FleetCard, EventsCard, TypeBreakdownCard, ZonesCard, type Kpi } from './components/DashboardParts'
import { useAuth } from './lib/auth'
import { useTheme } from './lib/theme'
import {
  IconAlert,
  IconCheck,
  IconCrosshair,
  IconLayers,
  IconLogout,
  IconMapPin,
  IconMoon,
  IconActivity,
  IconDashboard,
  IconMaximize,
  IconPanelLeft,
  IconPin,
  IconSun,
  IconPolygon,
  IconTruck,
  IconUsers,
} from './components/Icons'
import type { MapInset } from './components/EntityMap'
import './App.css'
import './modern.css'
import './vibrant.css'
import './premium.css'
import './dashboard.css'
import './shell.css'
import './vehicle-modern.css'
import './ui-premium.css'
import './flat.css'
import './fleet-final.css'
import './theme-red.css'
import './mobile-fix.css'

type Toast = { kind: 'success' | 'error'; text: string } | null
type ApiState = 'checking' | 'online' | 'offline'

const API_STATE_UI: Record<ApiState, { label: string; title: string; cls: string }> = {
  checking: { label: 'API checking', title: 'Checking the backend connection', cls: 'checking' },
  online: { label: 'API online', title: 'Connected to the backend', cls: 'up' },
  offline: { label: 'API offline', title: 'The backend is not reachable', cls: 'down' },
}


/** How many crossings the live panel keeps on screen. */
const GEOFENCE_HISTORY_LIMIT = 12

/**
 * Newest first, one row per crossing: the stored id is the key, so a crossing
 * that arrives over the live feed and the same crossing in the history are one
 * row, not two.
 */
function mergeGeofenceEvents(incoming: GeofenceEvent[], existing: GeofenceEvent[]): GeofenceEvent[] {
  const seen = new Set<number>()
  const out: GeofenceEvent[] = []
  for (const ev of [...incoming, ...existing]) {
    if (seen.has(ev.id)) continue
    seen.add(ev.id)
    out.push(ev)
  }
  return out.slice(0, GEOFENCE_HISTORY_LIMIT)
}






type Tab = 'overview' | 'entities' | 'zones' | 'vehicles' | 'users'
const NAV: { key: Tab; label: string; title: string; sub: string }[] = [
  { key: 'overview', label: 'Overview', title: 'Overview', sub: 'Your whole operation at a glance' },
  { key: 'entities', label: 'Places', title: 'Places', sub: 'Browse, search and manage locations' },
  { key: 'zones', label: 'Zones', title: 'Zones', sub: 'Draw and manage work areas' },
  { key: 'vehicles', label: 'Fleet', title: 'Live fleet', sub: 'Track units and plan delivery routes' },
  { key: 'users', label: 'Users', title: 'Users', sub: 'Register accounts and set their user type' },
]

/** Only admins manage accounts, so the tab is hidden for everyone else. */
const ADMIN_TABS: Tab[] = ['users']

/** The map sits in its own card now, so no part of it is hidden behind a panel. */
const NO_INSET: MapInset = { left: 0, bottom: 0 }

const RAIL_KEY = 'groundwork:rail-collapsed'

export default function App() {
  const { user, logout, role, isAdmin } = useAuth()
  // Only admins may write; viewer and driver get the map and the data, read-only.
  const canEdit = isAdmin()
  const [entities, setEntities] = useState<Entity[]>([])
  const [visibleEntities, setVisibleEntities] = useState<Entity[]>([])
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [apiState, setApiState] = useState<ApiState>('checking')
  const [toast, setToast] = useState<Toast>(null)
  const { theme, toggleTheme } = useTheme()

  // Zone state
  const [zones, setZones] = useState<ZoneFeature[]>([])
  const [zonesLoading, setZonesLoading] = useState(true)
  const [zonesError, setZonesError] = useState<string | null>(null)
  const [selectedZoneId, setSelectedZoneId] = useState<number | null>(null)
  const [drawMode, setDrawMode] = useState(false)
  // Drawing a polygon and dragging its vertices are write actions, so the map
  // stays read-only for everyone but an admin.
  const zoneDrawMode = canEdit && drawMode
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
  // The vehicle the operator is looking at in the live panel.
  const [selectedVehicleId, setSelectedVehicleId] = useState<number | null>(null)
  // Playback of a recorded trip: the track of a unit and the ghost marker
  // that walks along it.
  const playback = usePlayback()
  const [streamState, setStreamState] = useState<StreamState>('connecting')
  // Every account in the workspace. The fleet form picks drivers out of this
  // list and the admin "Users" page registers and edits them.
  const [accounts, setAccounts] = useState<UserAccount[]>([])
  const [accountsLoading, setAccountsLoading] = useState(true)
  const [accountsError, setAccountsError] = useState<string | null>(null)
  const [accountToDelete, setAccountToDelete] = useState<UserAccount | null>(null)
  const [accountDeleting, setAccountDeleting] = useState(false)
  const [vehicleToDelete, setVehicleToDelete] = useState<Vehicle | null>(null)
  const [vehicleDeleting, setVehicleDeleting] = useState(false)

  // Zone boundary crossings. The history arrives on mount; every new crossing
  // is pushed over the live feed and lands on top of the list.
  const [geofenceEvents, setGeofenceEvents] = useState<GeofenceEvent[]>([])

  // Delivery routes: one active path per vehicle, drawn under the markers.
  // The draft holds the two stops picked on the map until they are saved.
  const [routes, setRoutes] = useState<VehicleRoute[]>([])
  const [routeDraft, setRouteDraft] = useState<{
    origin: RouteStop | null
    destination: RouteStop | null
    /** True while the origin is still the unit's own position. */
    originIsDefault: boolean
  }>({
    origin: null,
    destination: null,
    originIsDefault: false,
  })
  const [routePick, setRoutePick] = useState<'origin' | 'destination' | null>(null)
  /** A plan is on its way to the server; the form waits for the line. */
  const [routeSaving, setRouteSaving] = useState(false)
  /** The route the camera has to show: the one that was just planned. */
  const [routeFocus, setRouteFocus] = useState<{ id: number; n: number } | null>(null)

  // Which page of the dashboard is open, and whether the nav rail is slimmed to icons.
  const [view, setView] = useState<Tab>('overview')
  const [railCollapsed, setRailCollapsed] = useState(() => {
    try {
      return localStorage.getItem(RAIL_KEY) === '1'
    } catch {
      return false
    }
  })
  const toggleRail = () =>
    setRailCollapsed((c) => {
      try {
        localStorage.setItem(RAIL_KEY, c ? '0' : '1')
      } catch {
        /* storage unavailable */
      }
      return !c
    })
  // Phone: the map above the list can be stretched to see more of it.
  const [mapTall, setMapTall] = useState(false)
  // Phone: workspace pages show either the list or the map, never both squeezed together.
  const [mobilePane, setMobilePane] = useState<'list' | 'map'>('list')

  // Live tracking camera: follow the selected unit, and a counter the map
  // watches to fit the whole fleet into view.
  const [following, setFollowing] = useState(false)
  const [fitFleetSignal, setFitFleetSignal] = useState(0)

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

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const showToast = useCallback((kind: 'success' | 'error', text: string) => {
    setToast({ kind, text })
  }, [])

  // The crossing history of the live panel. The stored id is the key, so a
  // crossing that arrives over the feed and the same crossing in the history
  // are one row, not two.
  useEffect(() => {
    let alive = true
    api
      .listGeofenceEvents({ limit: GEOFENCE_HISTORY_LIMIT })
      .then((stored) => {
        if (!alive) return
        setGeofenceEvents((prev) => mergeGeofenceEvents(stored, prev))
      })
      .catch(() => {
        /* the panel stays empty; the live feed still reports new crossings */
      })
    return () => {
      alive = false
    }
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

    /**
     * Merge fresh counters into the zones that are already on the map.
     *
     * The zone list carries the geometry of every polygon, which is far too
     * heavy to reload whenever a place is added or a unit crosses a border.
     * The counters live in their own place, so they can be refreshed on their
     * own without touching the shapes.
     */
    const applyZoneCounts = useCallback((counts: ZoneCount[]) => {
      if (!counts.length) return
      const byId = new Map(counts.map((c) => [c.id, c]))
      setZones((prev) => {
        let changed = false
        const next = prev.map((z) => {
          const c = byId.get(z.properties.id)
          if (!c) return z
          if (z.properties.entity_count === c.entity_count && z.properties.vehicle_count === c.vehicle_count) {
            return z
          }
          changed = true
          return {
            ...z,
            properties: {
              ...z.properties,
              entity_count: c.entity_count,
              vehicle_count: c.vehicle_count,
            },
          }
        })
        return changed ? next : prev
      })
    }, [])

    // The counters of the zones, refreshed on their own every few seconds.
    useEffect(() => {
      let alive = true
      const load = () => {
        api
          .listZoneCounts()
          .then((counts) => {
            if (alive) applyZoneCounts(counts)
          })
          .catch(() => {
            /* the map keeps the last known numbers */
          })
      }
      load()
      const id = window.setInterval(load, 15000)
      return () => {
        alive = false
        window.clearInterval(id)
      }
    }, [applyZoneCounts])

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
        // Every unit carries the route it is following, so the map can draw
        // the lines right away.
        setRoutes((prev) => mergeRoutes(prev, activeRoutesOf(data.vehicles)))
      } catch (err) {
        setVehiclesError(err instanceof Error ? err.message : 'Could not load vehicles')
      } finally {
        setVehiclesLoading(false)
      }
    }, [])

    useEffect(() => {
      void refreshVehicles()
    }, [refreshVehicles])

    // The fleet form hands a unit over to a driver and the Users page edits the
    // same accounts, so one list serves both. Only admins may read it, so the
    // other roles skip the call.
    const refreshAccounts = useCallback(async () => {
      if (!canEdit) {
        setAccountsLoading(false)
        return
      }
      setAccountsLoading(true)
      try {
        setAccounts(await api.listUsers())
        setAccountsError(null)
      } catch (err) {
        setAccountsError(err instanceof Error ? err.message : 'Could not load accounts')
      } finally {
        setAccountsLoading(false)
      }
    }, [canEdit])

    useEffect(() => {
      void refreshAccounts()
    }, [refreshAccounts])

    /** Driver accounts a unit can be handed over to. */
    const drivers = useMemo(() => accounts.filter((u) => u.role === 'driver'), [accounts])

    // Routes are a separate list: the map draws them for the whole fleet, not
    // only for the selected unit.
    const refreshRoutes = useCallback(async () => {
      try {
        const data = await api.listRoutes()
        setRoutes((prev) => mergeRoutes(prev, data.routes))
      } catch {
        // A missing route list must not break the fleet view; the map simply
        // shows no paths until the next attempt.
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
              // The snapshot carries the active route of every unit.
              setRoutes((prev) => mergeRoutes(prev, activeRoutesOf(frame.vehicles)))
              return
            }
            if (frame.type === 'route') {
              setRoutes((prev) => mergeRoutes(prev, [frame.route]))
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
            if (frame.type === 'zones') {
              // The counters of the zones changed: a place was added, moved or
              // removed, or a unit crossed a border.
              applyZoneCounts(frame.zones)
              return
            }
            if (frame.type === 'vehicle_updated') {
              // A unit was added or edited somewhere: the list and the map
              // follow it without a reload.
              setVehicles((prev) => {
                const at = prev.findIndex((v) => v.id === frame.vehicle.id)
                if (at === -1) return [...prev, frame.vehicle]
                const next = prev.slice()
                next[at] = frame.vehicle
                return next
              })
              return
            }
            if (frame.type === 'vehicle_deleted') {
              setVehicles((prev) => prev.filter((v) => v.id !== frame.id))
              return
            }
            if (frame.type === 'geofence') {
              // A unit crossed a zone boundary: say it out loud and keep the
              // crossing history of the panel in step.
              const entering = frame.event === 'ENTER'
              showToast(
                entering ? 'success' : 'error',
                `${entering ? '🚚' : '⚠️'} ${frame.vehicle_name} ${entering ? 'MASUK ke' : 'KELUAR dari'} ${frame.zone_name}`,
              )
              setGeofenceEvents((prev) =>
                mergeGeofenceEvents(
                  [
                    {
                      id: frame.id,
                      vehicle_id: frame.vehicle_id,
                      vehicle_name: frame.vehicle_name,
                      zone_id: frame.zone_id,
                      zone_name: frame.zone_name,
                      event_type: frame.event,
                      created_at: frame.timestamp,
                    },
                  ],
                  prev,
                ),
              )
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
      [applyZoneCounts],
    )

    // Drop the selection when a unit leaves the fleet.
    useEffect(() => {
      if (selectedVehicleId == null) return
      if (!vehicles.some((v) => v.id === selectedVehicleId)) setSelectedVehicleId(null)
    }, [vehicles, selectedVehicleId])

    // Half-picked stops belong to one vehicle: switching units or closing the
    // picker must not leave a stray A/B marker on the map.
    useEffect(() => {
      setRouteDraft({ origin: null, destination: null, originIsDefault: false })
      setRoutePick(null)
    }, [selectedVehicleId])

    // Picking a unit (list or map) opens the live view for it and starts following.
    const selectVehicle = useCallback((id: number | null) => {
      setSelectedVehicleId(id)
      if (id == null) return
      setFollowing(true)
      setSelectedId(null)
      setView('vehicles')
    }, [])

    const handleRoutePickStart = (stage: 'origin' | 'destination') => {
      // One map mode at a time: arming a route stop disarms entity pinning.
      setPicking(false)
      setRoutePick(stage)
    }

    /**
     * Plan a route and draw it. The server answers with the road path
     * (OSRM) or the straight line when no engine could be reached, and the
     * camera moves to the new line so it is never drawn off screen.
     */
    const planRoute = useCallback(
      async (vehicleId: number, origin: RouteStop, destination: RouteStop) => {
        setRouteSaving(true)
        try {
          const route = await api.setVehicleRoute(vehicleId, { origin, destination })
          setRoutes((prev) => mergeRoutes(prev, [route]))
          setRouteDraft({ origin: null, destination: null, originIsDefault: false })
          setRoutePick(null)
          setRouteFocus((f) => ({ id: route.id, n: (f?.n ?? 0) + 1 }))
          showToast(
            'success',
            `Route planned to ${destination.name || 'the destination'} · ${formatDistance(route.distance_m)}`,
          )
        } catch (err) {
          showToast('error', err instanceof Error ? err.message : 'Could not plan the route')
          throw err
        } finally {
          setRouteSaving(false)
        }
      },
      [showToast],
    )

    /**
     * "Plan route" starts the plan at the unit's own position: the operator
     * only has to pick the destination on the map.
     */
    const handleRoutePlanStart = useCallback(
      (vehicleId: number) => {
        const unit = vehicles.find((v) => v.id === vehicleId)
        const origin: RouteStop | null =
          unit && hasPosition(unit)
            ? { lat: Number(unit.lat.toFixed(6)), lng: Number(unit.lng.toFixed(6)), name: unit.name }
            : null
        setRouteDraft({ origin, destination: null, originIsDefault: origin != null })
        setRoutePick(null)
      },
      [vehicles],
    )

    /**
     * A short label for a picked point: the closest place when the operator
     * picked next to one, a plain name otherwise. The fleet card says where
     * the unit is heading, so the target needs a name of its own.
     */
    const labelForPoint = useCallback(
      (lat: number, lng: number, fallback: string) => {
        let best: { name: string; d: number } | null = null
        for (const e of entities) {
          const d =
            Math.hypot(
              (e.latitude - lat) * 111.32,
              (e.longitude - lng) * 111.32 * Math.cos((lat * Math.PI) / 180),
            )
          if (!best || d < best.d) best = { name: e.name, d }
        }
        if (best && best.d <= 2) return best.name
        return fallback
      },
      [entities],
    )

    const handleRoutePoint = (lat: number, lng: number) => {
      if (!routePick) return
      const stop: RouteStop = {
        lat: Number(lat.toFixed(6)),
        lng: Number(lng.toFixed(6)),
        name: labelForPoint(lat, lng, routePick === 'destination' ? 'Destination' : 'Origin'),
      }
      setRouteDraft((prev) => ({
        ...prev,
        [routePick]: stop,
        // A hand-picked origin is not the unit's position any more, so the
        // plan waits for the "Build route" button instead of going out by
        // itself.
        originIsDefault: routePick === 'destination' ? prev.originIsDefault : false,
      }))
      setRoutePick(null)

      // Picking the destination is the whole job when the plan starts at the
      // unit's position: the request goes out right away and the line appears
      // on the map. A hand-picked origin waits for the "Build route" button,
      // so the operator can still check both stops first.
      if (routePick === 'destination' && routeDraft.originIsDefault && routeDraft.origin && selectedVehicleId != null) {
        void planRoute(selectedVehicleId, routeDraft.origin, stop).catch(() => {
          /* the toast already says what went wrong */
        })
      }
    }

    const handleRouteLabel = (stage: 'origin' | 'destination', name: string) => {
      setRouteDraft((prev) => {
        const current = prev[stage]
        if (!current) return prev
        return { ...prev, [stage]: { ...current, name } }
      })
    }

    const handleRouteSet = async (vehicleId: number, origin: RouteStop, destination: RouteStop) => {
      if (!canEdit) return
      await planRoute(vehicleId, origin, destination)
    }

    const handleRouteClear = async (vehicleId: number) => {
      if (!canEdit) return
      await api.clearVehicleRoute(vehicleId)
      setRoutes((prev) => prev.filter((r) => r.vehicle_id !== vehicleId))
      setRouteDraft({ origin: null, destination: null, originIsDefault: false })
      showToast('success', 'Route cleared')
    }

    // Driving a unit by hand: dropping its marker anywhere on the map reports
    // the new position, which the server stores, broadcasts and checks against
    // the zones. Admins drive any unit; a driver drives only their own.
    const canDriveVehicle = (v: Vehicle) =>
      canEdit || (role === 'driver' && v.driver_id != null && v.driver_id === user?.id)

    const handleDrive = async (id: number, lat: number, lng: number) => {
      const before = vehicles.find((v) => v.id === id)
      if (!before) return
      // A drag says where the unit is now, not how fast it got there: keep its
      // last reported speed so the readout stays plausible instead of guessing.
      const speed = before.status === 'moving' && before.speed_kmh > 0 ? before.speed_kmh : 30
      // The unit points at the place it was dropped, so the marker turns the
      // right way.
      const heading = hasPosition(before) ? bearingBetween(before, { lat, lng }) : null
      setVehicles((prev) =>
        prev.map((v) =>
          v.id === id
            ? { ...v, lat, lng, status: 'moving', speed_kmh: speed, heading, last_seen_at: new Date().toISOString() }
            : v,
        ),
      )
      try {
        const updated = await api.reportPosition(id, { lat, lng, speed_kmh: speed, status: 'moving', heading })
        setVehicles((prev) => prev.map((v) => (v.id === id ? updated : v)))
      } catch (err) {
        // The drop was never stored: put the unit back where it was.
        setVehicles((prev) => prev.map((v) => (v.id === id ? before : v)))
        showToast('error', err instanceof Error ? err.message : 'Could not move this vehicle')
      }
    }

    // Fleet changes: the list and the map update right away, so a unit that was
    // just added shows up on the map without a reload.
    const handleVehicleCreate = async (input: VehicleInput) => {
      if (!canEdit) return
      const created = await api.createVehicle(input)
      setVehicles((prev) =>
        prev.some((v) => v.id === created.id)
          ? prev.map((v) => (v.id === created.id ? created : v))
          : [...prev, created],
      )
      showToast('success', `${created.name} added to the fleet`)
    }

    const handleVehicleUpdate = async (id: number, input: VehicleInput) => {
      if (!canEdit) return
      const updated = await api.updateVehicle(id, input)
      setVehicles((prev) => prev.map((v) => (v.id === id ? updated : v)))
      showToast('success', `${updated.name} saved`)
    }

    const handleVehicleDelete = async () => {
      if (!canEdit || !vehicleToDelete) return
      setVehicleDeleting(true)
      try {
        await api.deleteVehicle(vehicleToDelete.id)
        setVehicles((prev) => prev.filter((v) => v.id !== vehicleToDelete.id))
        setVehicleToDelete(null)
        showToast('success', `${vehicleToDelete.name} removed from the fleet`)
      } catch (err) {
        showToast('error', err instanceof Error ? err.message : 'Could not delete the vehicle')
      } finally {
        setVehicleDeleting(false)
      }
    }

    // Account changes: the list feeds both the Users page and the fleet form's
    // driver picker, so a new driver is assignable straight away.
    const handleAccountCreate = async (input: UserInput) => {
      if (!canEdit) return
      const created = await api.createUser(input)
      setAccounts((prev) => [...prev, created])
      showToast('success', `${created.username} can now sign in as ${created.role}`)
    }

    const handleAccountUpdate = async (id: number, input: Partial<UserInput>) => {
      if (!canEdit) return
      const updated = await api.updateUser(id, input)
      setAccounts((prev) => prev.map((u) => (u.id === id ? updated : u)))
      showToast('success', `${updated.username} saved`)
    }

    const handleAccountDelete = async () => {
      if (!canEdit || !accountToDelete) return
      setAccountDeleting(true)
      try {
        await api.deleteUser(accountToDelete.id)
        setAccounts((prev) => prev.filter((u) => u.id !== accountToDelete.id))
        setAccountToDelete(null)
        showToast('success', `${accountToDelete.username} removed`)
      } catch (err) {
        showToast('error', err instanceof Error ? err.message : 'Could not delete the account')
      } finally {
        setAccountDeleting(false)
      }
    }

    // A zone built from a boundary arrives complete: refresh the list and open
    // it, so the operator sees exactly what was copied.
    const handleZoneCreated = useCallback(
      (zone: ZoneFeature) => {
        setZones((prev) => [...prev.filter((z) => z.properties.id !== zone.properties.id), zone])
        setSelectedZoneId(zone.properties.id)
        setView('zones')
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

  /** The colour of every unit's delivery line, keyed by vehicle id. */
  const routeColors = useMemo(() => {
    const m = new Map<number, string>()
    for (const v of vehicles) if (v.route_color) m.set(v.id, v.route_color)
    return m
  }, [vehicles])

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
  // The Overview has no places list to filter, so it shows every place.
  const mapEntities = useMemo(() => {
    if (view === 'overview') return entities
    if (selected && !visibleEntities.some((e) => e.id === selected.id)) {
      return [...visibleEntities, selected]
    }
    return visibleEntities
  }, [view, entities, visibleEntities, selected])

  // While the form is docked over the list, the list underneath must not be reachable by keyboard.
  useEffect(() => {
    listSlotRef.current?.toggleAttribute('inert', formOpen)
  }, [formOpen])

  const openCreate = () => {
    if (!canEdit) return
    setFormMode('create')
    setServerFields({})
    setDraftCoords(null)
    setPicking(true)
    setFormOpen(true)
  }

  const openEdit = () => {
    if (!canEdit || !selected) return
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
    if (!canEdit) return
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
        if (!canEdit) return
        const created = await api.createZone(input)
        showToast('success', `Zone "${created.properties.name}" created`)
        await refreshZones()
      }, [refreshZones, canEdit])

      const handleZoneUpdate = useCallback(async (id: number, input: ZoneInput) => {
        if (!canEdit) return
        const updated = await api.updateZone(id, input)
        showToast('success', `Zone "${updated.properties.name}" updated`)
        await refreshZones()
      }, [refreshZones, canEdit])

      const handleZoneDelete = useCallback(async (id: number) => {
        if (!canEdit) return
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
      }, [refreshZones, selectedZoneId, canEdit])

      // A polygon drawn on the map becomes the working geometry of the form.
      const handleZoneGeometryCreated = useCallback((geom: ZoneGeometry) => {
        if (!canEdit) return
        setZoneDraftGeometry(geom)
        setZoneEditingId(null)
        setZoneFormOpen(true)
      }, [canEdit])

      // Dragging a vertex of an existing zone saves the new shape, debounced so
      // a drag produces one request instead of one per pointer move.
      const zoneEditTimer = useRef<number | null>(null)
      const zoneEditPending = useRef<{ id: number; geom: ZoneGeometry } | null>(null)

      const handleZoneGeometryEdited = useCallback(
        (id: number, geom: ZoneGeometry) => {
          if (!canEdit) return
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
        [zones, refreshZones, showToast, canEdit],
      )

      useEffect(
        () => () => {
          if (zoneEditTimer.current !== null) window.clearTimeout(zoneEditTimer.current)
        },
        [],
      )

      const handleZoneImport = useCallback(async (file: File): Promise<ZoneImportReport | null> => {
        if (!canEdit) return null
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
      }, [refreshZones, canEdit])

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
    if (!canEdit || !selected) return
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

  // The detail card and the form take over the list pane while they are open.
  const covered = selected != null || formOpen

  const placesActive = entities.filter((e) => e.status === 'active').length
  const zoneArea = zones.reduce((sum, z) => sum + (z.properties.area_km2 || 0), 0)
  const online = fleet.moving + fleet.idle
  const lastEvent = geofenceEvents[0]

  const kpis: Kpi[] = [
    {
      key: 'fleet',
      label: 'Fleet online',
      value: String(online),
      unit: `/ ${vehicles.length}`,
      sub: (
        <>
          <b>{fleet.moving}</b> moving · <b>{fleet.idle}</b> idle · <b>{fleet.offline}</b> offline
        </>
      ),
      tone: 'ok',
      icon: <IconTruck width={18} height={18} />,
      onClick: () => setView('vehicles'),
    },
    {
      key: 'places',
      label: 'Places',
      value: String(entities.length),
      sub: (
        <>
          <b>{placesActive}</b> active · <b>{entities.length - placesActive}</b> inactive
        </>
      ),
      tone: 'accent',
      icon: <IconPin width={18} height={18} />,
      onClick: () => setView('entities'),
    },
    {
      key: 'zones',
      label: 'Zones',
      value: String(zones.length),
      sub: <>{zoneArea.toLocaleString(undefined, { maximumFractionDigits: 0 })} km² covered</>,
      tone: 'info',
      icon: <IconPolygon width={18} height={18} />,
      onClick: () => setView('zones'),
    },
    {
      key: 'events',
      label: 'Zone crossings',
      value: String(geofenceEvents.length),
      sub: lastEvent ? (
        <>
          Last: <b>{lastEvent.vehicle_name}</b> {lastEvent.event_type === 'ENTER' ? 'in' : 'out'}
        </>
      ) : (
        'Nothing recorded yet'
      ),
      tone: 'warn',
      icon: <IconActivity width={18} height={18} />,
      onClick: () => setView('vehicles'),
    },
  ]

  const page = NAV.find((n) => n.key === view) ?? NAV[0]
  const navCount = (k: Tab): number | null =>
    k === 'entities'
      ? entities.length
      : k === 'zones'
        ? zones.length
        : k === 'vehicles'
          ? vehicles.length
          : k === 'users'
            ? accounts.length
            : null

  /** Tabs the signed-in role may open; the sidebar renders only these. */
  const visibleNav = useMemo(
    () => NAV.filter((n) => canEdit || !ADMIN_TABS.includes(n.key)),
    [canEdit],
  )

  const goTo = (t: Tab) => {
    // A viewer must never land on an admin screen, even from stale state.
    if (!canEdit && ADMIN_TABS.includes(t)) return
    setView(t)
    setMobilePane('list')
    if (t !== 'overview') setMapTall(false)
  }

  // Pinning a place, drawing a zone or setting a route stop needs the map in front.
  useEffect(() => {
    if (picking || zoneDrawMode || routePick !== null) setMobilePane('map')
  }, [picking, zoneDrawMode, routePick])

  const apiUi = API_STATE_UI[apiState]
  const streamLabel = streamState === 'live' ? 'Live' : streamState === 'connecting' ? 'Connecting' : 'Reconnecting'
  // Username is always set; the email is optional on accounts created by an admin.
  const accountLabel = user?.name?.trim() || user?.username || user?.email || 'Signed in'
  const initial = accountLabel.charAt(0).toUpperCase()

  const themeBtn = (
    <button
      className="icon-btn"
      onClick={toggleTheme}
      aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
      title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
    >
      {theme === 'dark' ? <IconSun /> : <IconMoon />}
    </button>
  )
  const logoutBtn = (
    <button className="icon-btn" onClick={logout} aria-label="Sign out" title={user ? `Sign out ${user.email}` : 'Sign out'}>
      <IconLogout />
    </button>
  )

  return (
    <div className={`dash${railCollapsed ? ' rail-slim' : ''}`} data-view={view}>
      {/* ---- Navigation rail (desktop / tablet) ---------------------------- */}
      <aside className="rail" aria-label="Main">
        <div className="rail-head">
          <span className="brand-mark">
            <IconLayers width={20} height={20} />
          </span>
          <div className="rail-brand">
            <h1>Groundwork</h1>
            <span>Field operations, mapped</span>
          </div>
        </div>

        <nav className="rail-nav" aria-label="Workspace">
          <span className="rail-section">Workspace</span>
          {visibleNav.map((n) => {
            const Icon =
              n.key === 'overview'
                ? IconDashboard
                : n.key === 'entities'
                  ? IconPin
                  : n.key === 'zones'
                    ? IconPolygon
                    : n.key === 'users'
                      ? IconUsers
                      : IconTruck
            const count = navCount(n.key)
            return (
              <button
                key={n.key}
                type="button"
                className={`rail-item${view === n.key ? ' active' : ''}`}
                aria-current={view === n.key ? 'page' : undefined}
                onClick={() => goTo(n.key)}
                title={n.label}
              >
                <Icon width={19} height={19} />
                <span className="rail-label">{n.label}</span>
                {count != null && (
                  <span className={`rail-count${n.key === 'vehicles' && streamState === 'live' ? ' live' : ''}`}>{count}</span>
                )}
              </button>
            )
          })}
        </nav>

        <div className="rail-foot">
          <div className="user-card">
            <span className="avatar" aria-hidden="true">
              {initial}
            </span>
            <div className="user-meta rail-label">
              <b title={accountLabel}>{accountLabel}</b>
              <span className={`role-badge ${role}`}>{role}</span>
            </div>
          </div>
          <div className="rail-tools">
            <button
              type="button"
              className="rail-tool"
              onClick={toggleTheme}
              aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
              title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
            >
              {theme === 'dark' ? <IconSun width={17} height={17} /> : <IconMoon width={17} height={17} />}
              <span className="rail-label">{theme === 'dark' ? 'Light' : 'Dark'}</span>
            </button>
            <button
              type="button"
              className="rail-tool rail-toggle"
              onClick={toggleRail}
              aria-label={railCollapsed ? 'Expand the menu' : 'Collapse the menu'}
              title={railCollapsed ? 'Expand the menu' : 'Collapse the menu'}
            >
              <IconPanelLeft width={17} height={17} />
              <span className="rail-label">{railCollapsed ? 'Expand' : 'Collapse'}</span>
            </button>
          </div>
          <button
            type="button"
            className="rail-logout"
            onClick={logout}
            aria-label="Log out"
            title={user ? `Log out ${accountLabel}` : 'Log out'}
          >
            <IconLogout width={18} height={18} />
            <span className="rail-label">Log out</span>
          </button>
        </div>
      </aside>

      <div className="main">
        {/* ---- Top bar ------------------------------------------------------ */}
        <header className="topbar">
          <span className="brand-mark topbar-mark">
            <IconLayers width={18} height={18} />
          </span>
          <div className="topbar-title">
            <h2>{page.title}</h2>
            <p>{page.sub}</p>
          </div>
          <div className="topbar-tools">
            {vehicles.length > 0 && (
              <span className={`chip stream ${streamState}`} title="Live vehicle feed">
                <i aria-hidden="true" />
                {streamLabel}
              </span>
            )}
            <span className={`chip api ${apiUi.cls}`} title={apiUi.title}>
              <i aria-hidden="true" />
              <span className="chip-text">{apiUi.label}</span>
            </span>
            <span className="topbar-mobile-tools">
              {themeBtn}
              {logoutBtn}
            </span>
          </div>
        </header>

        {/* ---- Stage -------------------------------------------------------- */}
        <main className="stage" data-view={view} data-tall={mapTall ? 'true' : 'false'} data-mpane={mobilePane}>
          {view !== 'overview' && view !== 'users' && (
            <div className="pane-switch" role="tablist" aria-label="Show the list or the map">
              <button
                type="button"
                role="tab"
                aria-selected={mobilePane === 'list'}
                className={mobilePane === 'list' ? 'on' : ''}
                onClick={() => setMobilePane('list')}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                  <path d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01" />
                </svg>
                {page.label}
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={mobilePane === 'map'}
                className={mobilePane === 'map' ? 'on' : ''}
                onClick={() => setMobilePane('map')}
              >
                <IconMapPin width={16} height={16} />
                Map
              </button>
            </div>
          )}
          {view === 'overview' && (
            <div className="stage-kpis">
              <KpiGrid items={kpis} />
            </div>
          )}

          {view !== 'users' && (
          <section className="map-card" aria-label="Map">
            <EntityMap
              entities={mapEntities}
              selected={selected}
              onSelect={(id) => {
                setSelectedId(id)
                if (id != null && view === 'overview') setView('entities')
                if (id != null) setMobilePane('list')
              }}
              picking={picking && formOpen}
              draftCoords={draftCoords}
              onPick={(lat, lng) => setDraftCoords({ latitude: lat, longitude: lng })}
              theme={theme}
              // Zones
              zones={zones}
              selectedZoneId={selectedZoneId}
              onZoneSelect={(id) => {
                setSelectedZoneId(id)
                if (id != null && view === 'overview') setView('zones')
                if (id != null) setMobilePane('list')
              }}
              drawMode={zoneDrawMode}
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
              onVehicleSelect={(id) => {
                selectVehicle(id)
                if (id != null) setMobilePane('list')
              }}
              vehiclesVisible={vehiclesVisible}
              canDrive={canDriveVehicle}
              onDrive={handleDrive}
              following={following}
              onFollowChange={setFollowing}
              fitFleetSignal={fitFleetSignal}
              inset={NO_INSET}
              // Delivery routes (from -> to)
              routes={routes}
              routesVisible={vehiclesVisible}
              routeColors={routeColors}
              routeDraft={routeDraft}
              // Playback of a recorded trip
              playback={playback}
              routePick={routePick !== null}
              onRoutePoint={handleRoutePoint}
              focusRoute={routeFocus}
            />
            {view !== 'overview' && (
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
            )}

            {view === 'vehicles' && vehiclesVisible && vehicles.length > 0 && (
              <div className="live-hud" role="status" aria-label="Fleet status">
                <span className={`hud-live ${streamState}`}>
                  <i aria-hidden="true" />
                  {streamLabel}
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

            <button
              type="button"
              className="map-expand"
              onClick={() => setMapTall((t) => !t)}
              aria-pressed={mapTall}
              aria-label={mapTall ? 'Shrink the map' : 'Enlarge the map'}
            >
              <IconMaximize width={16} height={16} />
              <span>{mapTall ? 'Less map' : 'More map'}</span>
            </button>

            {routePick !== null && (
        <div className="route-pick-hint" role="status">
          <IconCrosshair width={16} height={16} />
          <span>Click the map to set {routePick === 'origin' ? 'the origin (A)' : 'the destination (B)'}</span>
          <button type="button" onClick={() => setRoutePick(null)}>
            Cancel
          </button>
        </div>
      )}
          </section>
          )}

          {view === 'overview' ? (
            <>
              <div className="stage-side">
                <FleetCard vehicles={vehicles} counts={fleet} onSelect={selectVehicle} onOpen={() => goTo('vehicles')} />
              </div>
              <div className="stage-extra">
                <EventsCard events={geofenceEvents} onOpen={() => goTo('vehicles')} />
                <TypeBreakdownCard entities={entities} onOpen={() => goTo('entities')} />
                <ZonesCard
                  zones={zones}
                  onSelect={(id) => {
                    setSelectedZoneId(id)
                    goTo('zones')
                  }}
                  onOpen={() => goTo('zones')}
                />
              </div>
            </>
          ) : (
            <section className={covered ? 'panel-pane is-covered' : 'panel-pane'} aria-label={page.title}>
              <div className="pane-body" ref={listSlotRef}>
            {view === 'users' && canEdit ? (
              <UserPanel
                users={accounts}
                loading={accountsLoading}
                error={accountsError}
                currentUserId={user?.id ?? null}
                onRetry={() => void refreshAccounts()}
                onCreate={handleAccountCreate}
                onUpdate={handleAccountUpdate}
                onDelete={setAccountToDelete}
              />
            ) : view === 'entities' ? (
              <EntityList
                entities={entities}
                selectedId={selectedId}
                loading={loading}
                error={loadError}
                onSelect={setSelectedId}
                onAdd={openCreate}
                onRetry={refresh}
                canEdit={canEdit}
                onVisibleEntitiesChange={setVisibleEntities}
                scopeName={
                  selectedZoneId != null
                    ? (zones.find((z) => z.properties.id === selectedZoneId)?.properties.name ?? null)
                    : null
                }
                onClearScope={() => setSelectedZoneId(null)}
              />
            ) : view === 'zones' ? (
              <>
                {canEdit && (
                <RegionZonePicker onCreated={handleZoneCreated} />
                )}
                <ZonePanel
                zones={zones}
                selectedId={selectedZoneId}
                loading={zonesLoading}
                error={zonesError}
                drawMode={zoneDrawMode}
                onDrawModeChange={(v) => canEdit && setDrawMode(v)}
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
                canEdit={canEdit}
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
                canDrive={canDriveVehicle}
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
                onPlanStart={handleRoutePlanStart}
                routeBusy={routeSaving}
                onClearDraft={() => {
                  setRouteDraft({ origin: null, destination: null, originIsDefault: false })
                  setRoutePick(null)
                }}
                onRouteSet={handleRouteSet}
                onRouteClear={handleRouteClear}
                canEdit={canEdit}
                onCreate={handleVehicleCreate}
                onUpdate={handleVehicleUpdate}
                onDelete={(v) => setVehicleToDelete(v)}
                drivers={drivers}
                events={geofenceEvents}
                playback={playback}
              />
            )}
              </div>

              {!formOpen && (
                <EntityDetail
                  entity={selected}
                  deleting={deleting}
                  onEdit={openEdit}
                  onDelete={() => setConfirmOpen(true)}
                  onClose={() => setSelectedId(null)}
                  canEdit={canEdit}
                />
              )}

      {canEdit && (
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
      )}
            </section>
          )}
        </main>
      </div>

      {/* ---- Bottom navigation (phone) ------------------------------------- */}
      <nav className="bottom-nav" aria-label="Workspace">
        {visibleNav.map((n) => {
          const Icon =
            n.key === 'overview'
              ? IconDashboard
              : n.key === 'entities'
                ? IconPin
                : n.key === 'zones'
                  ? IconPolygon
                  : n.key === 'users'
                    ? IconUsers
                    : IconTruck
          const count = navCount(n.key)
          return (
            <button
              key={n.key}
              type="button"
              className={view === n.key ? 'active' : ''}
              aria-current={view === n.key ? 'page' : undefined}
              onClick={() => goTo(n.key)}
            >
              <span className="bn-icon">
                <Icon width={22} height={22} />
                {count != null && count > 0 && <em className={n.key === 'vehicles' && streamState === 'live' ? 'live' : ''}>{count}</em>}
              </span>
              <span>{n.label}</span>
            </button>
          )
        })}
      </nav>

      {canEdit && (
      <ConfirmDialog
        open={confirmOpen}
        title="Delete this entity?"
        message={selected ? `"${selected.name}" will be removed for good.` : ''}
        confirmLabel="Delete"
        busy={deleting}
        onConfirm={handleDelete}
        onCancel={() => setConfirmOpen(false)}
      />
      )}

      {canEdit && vehicleToDelete && (
        <ConfirmDialog
          open
          title="Delete this vehicle?"
          message={`"${vehicleToDelete.name}" (${vehicleToDelete.code}) will be removed for good.`}
          confirmLabel="Delete"
          busy={vehicleDeleting}
          onConfirm={handleVehicleDelete}
          onCancel={() => setVehicleToDelete(null)}
        />
      )}

      {canEdit && accountToDelete && (
        <ConfirmDialog
          open
          title="Delete this account?"
          message={`"${accountToDelete.username}" will lose access right away. Units assigned to this driver stay in the fleet, just without a driver.`}
          confirmLabel="Delete"
          busy={accountDeleting}
          onConfirm={handleAccountDelete}
          onCancel={() => setAccountToDelete(null)}
        />
      )}

      {toast && (
        <div className={`toast ${toast.kind}`} role={toast.kind === 'error' ? 'alert' : 'status'}>
          {toast.kind === 'error' ? <IconAlert width={18} height={18} /> : <IconCheck width={18} height={18} />}
          <span>{toast.text}</span>
        </div>
      )}
    </div>
  )
}
