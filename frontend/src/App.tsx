import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './lib/api'
import type {
  Entity,
  EntityFieldErrors,
  EntityInput,
  AuditLog,
  GeofenceEvent,
  ZoneCount,
  ZoneFeature,
  ZoneGeometry,
  ZoneInput,
  ZoneCollection,
  ZoneImportReport,
  RegionFeature,
  RegionCollection,
  RouteDraft,
  RoutePick,
  RouteStop,
  Task,
  TaskInput,
  TaskStatus,
  UserAccount,
  UserInput,
  Vehicle,
  VehicleInput,
  VehicleRoute,
  Overview,
  Report,
} from './lib/types'
import { activeRoutesOf, bearingBetween, formatDistance, hasPosition, mergeRoutes, taskStatusLabel } from './lib/types'
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
import PlaybackDialog from './components/PlaybackDialog'
import NotificationBell from './components/NotificationBell'
import ActivityLogPanel, { AUDIT_PAGE_SIZE } from './components/ActivityLogPanel'
import TaskBoard from './components/TaskBoard'
import ReportsPanel, { type ReportPreset } from './components/ReportsPanel'
import { openVehicleStream, type StreamState } from './lib/vehicleStream'
import { usePlayback } from './lib/playback'
import { FleetCard, TypeBreakdownCard, ZonesCard } from './components/DashboardParts'
import { OverviewStatCards, OverviewActivityCard } from './components/OverviewDashboard'
import { useAuth } from './lib/auth'
import { useTheme } from './lib/theme'
import {
  IconAlert,
  IconCheck,
  IconClipboard,
  IconCrosshair,
  IconHistory,
  IconMore,
  IconLayers,
  IconLogout,
  IconMapPin,
  IconMoon,
  IconDashboard,
  IconMaximize,
  IconPanelLeft,
  IconPin,
  IconSun,
  IconPolygon,
  IconTruck,
  IconUsers,
  IconFile,
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
import './notify.css'
import './activity.css'
import './driver.css'
import './polish.css'
import './tasks.css'
import './modern-ui.css'
import './motion.css'
import './reports.css'
import './responsive-final.css'

type Toast = { kind: 'success' | 'error' | 'alert'; text: string; id: number } | null
type ApiState = 'checking' | 'online' | 'offline'

const API_STATE_UI: Record<ApiState, { label: string; title: string; cls: string }> = {
  checking: { label: 'API checking', title: 'Checking the backend connection', cls: 'checking' },
  online: { label: 'API online', title: 'Connected to the backend', cls: 'up' },
  offline: { label: 'API offline', title: 'The backend is not reachable', cls: 'down' },
}

/** The label the toast shows above its message. */
const TOAST_LABEL: Record<'success' | 'error' | 'alert', string> = {
  success: 'Berhasil',
  error: 'Gagal',
  alert: 'Peringatan',
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

/**
 * A day of the report, counted from today: -1 is yesterday, 0 is today.
 * The days are the way the period is picked, so they are plain days.
 */
function reportDay(offset: number): string {
  const d = new Date()
  d.setDate(d.getDate() + offset)
  const pad = (v: number) => String(v).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}






type Tab = 'overview' | 'entities' | 'zones' | 'vehicles' | 'tasks' | 'reports' | 'users' | 'activity'
const NAV: { key: Tab; label: string; title: string; sub: string }[] = [
  { key: 'overview', label: 'Overview', title: 'Overview', sub: 'Your whole operation at a glance' },
  { key: 'entities', label: 'Places', title: 'Places', sub: 'Browse, search and manage locations' },
  { key: 'zones', label: 'Zones', title: 'Zones', sub: 'Draw and manage work areas' },
  { key: 'vehicles', label: 'Fleet', title: 'Fleet', sub: 'Live fleet: track units and plan delivery routes' },
  { key: 'tasks', label: 'Tasks', title: 'Tasks', sub: 'The work orders of the field: who does what, and where' },
  { key: 'reports', label: 'Reports', title: 'Reports', sub: 'What the fleet did in a period, and the numbers as a CSV' },
  { key: 'users', label: 'Users', title: 'Users', sub: 'Register accounts and set their user type' },
  { key: 'activity', label: 'Activity Log', title: 'Activity Log', sub: 'Who changed what, and when' },
]

/** Only admins manage accounts and read the trail, so those tabs are hidden. */
const ADMIN_TABS: Tab[] = ['users', 'activity']

/** The tabs that stay on the phone dock; every other tab opens from "More". */
const DOCK_MAIN: Tab[] = ['overview', 'entities', 'vehicles', 'tasks']

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
  // The unit whose trip is being played back, for the name on the dialog.
  const playbackVehicle = useMemo(
    () => (playback.vehicleId == null ? null : vehicles.find((v) => v.id === playback.vehicleId) ?? null),
    [playback.vehicleId, vehicles],
  )

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

  // The trail of successful writes, shown in the admin "Activity Log" tab.
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([])
  const [auditTotal, setAuditTotal] = useState(0)
  const [auditLoading, setAuditLoading] = useState(true)
  const [auditError, setAuditError] = useState<string | null>(null)
  const [auditAction, setAuditAction] = useState('')
  const [auditQuery, setAuditQuery] = useState('')
  const [auditOffset, setAuditOffset] = useState(0)

  // Zone boundary crossings. The history arrives on mount; every new crossing
  // is pushed over the live feed and lands on top of the list.
  const [geofenceEvents, setGeofenceEvents] = useState<GeofenceEvent[]>([])

  // Work orders: the jobs the field has to do. The list is asked for when
  // the tab opens and again after every change.
  const [tasks, setTasks] = useState<Task[]>([])
  const [tasksLoading, setTasksLoading] = useState(true)
  const [tasksError, setTasksError] = useState<string | null>(null)
  const [taskToDelete, setTaskToDelete] = useState<Task | null>(null)
  const [taskDeleting, setTaskDeleting] = useState(false)

  // The operational report: what the fleet did in a period. The server adds
  // the numbers up, so the browser only draws them.
  const [report, setReport] = useState<Report | null>(null)
  const [reportLoading, setReportLoading] = useState(false)
  const [reportError, setReportError] = useState<string | null>(null)
  const [reportPreset, setReportPreset] = useState<ReportPreset>('week')
  const [reportFrom, setReportFrom] = useState(() => reportDay(-6))
  const [reportTo, setReportTo] = useState(() => reportDay(0))
  const [reportVehicle, setReportVehicle] = useState('')
  const [reportExporting, setReportExporting] = useState(false)

  // The aggregate of the day behind the Overview tab: the distance the fleet
  // drove, the fleet counted by status and the crossings of today. The server
  // adds it up, so the browser only draws it.
  const [overview, setOverview] = useState<Overview | null>(null)
  const [overviewLoading, setOverviewLoading] = useState(true)
  const [overviewError, setOverviewError] = useState<string | null>(null)

  // Delivery routes: one active path per vehicle, drawn under the markers.
  // The draft holds the origin and the stops picked on the map until they
  // are saved.
  const [routes, setRoutes] = useState<VehicleRoute[]>([])
  const [routeDraft, setRouteDraft] = useState<RouteDraft>({
    origin: null,
    stops: [],
  })
  const [routePick, setRoutePick] = useState<RoutePick | null>(null)
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

  // The playback is watched on the map, so a phone switches to the map pane
  // when a trip is opened.
  useEffect(() => {
    if (playback.vehicleId != null) setMobilePane('map')
  }, [playback.vehicleId])

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
    // A toast answers an action, so it goes away on its own. The warnings of
    // the fleet do not live here: they stay in the bell of the top bar.
    if (!toast) return
    const t = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const showToast = useCallback((kind: 'success' | 'error' | 'alert', text: string) => {
    setToast({ kind, text, id: Date.now() })
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

  /**
   * The aggregate of the day. The server adds the kilometres up over the
   * reported positions, so the browser never sees the raw history.
   */
  const refreshOverview = useCallback(async () => {
    setOverviewLoading(true)
    try {
      const data = await api.getOverview()
      setOverview(data)
      setOverviewError(null)
    } catch (err) {
      setOverviewError(err instanceof Error ? err.message : 'Could not load the overview.')
    } finally {
      setOverviewLoading(false)
    }
  }, [])

  // The overview opens with the page and then keeps itself fresh: the day
  // does not stand still, so the numbers are pulled in again every half
  // minute.
  useEffect(() => {
    void refreshOverview()
    const id = window.setInterval(() => void refreshOverview(), 30000)
    return () => window.clearInterval(id)
  }, [refreshOverview])

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

    // The work orders of the board. They are asked for on mount, so the
    // count in the sidebar is right, and again whenever the tab opens.
    const refreshTasks = useCallback(async () => {
      setTasksLoading(true)
      try {
        setTasks(await api.listTasks())
        setTasksError(null)
      } catch (err) {
        setTasksError(err instanceof Error ? err.message : 'Could not load the tasks.')
      } finally {
        setTasksLoading(false)
      }
    }, [])

    useEffect(() => {
      void refreshTasks()
    }, [refreshTasks])

    useEffect(() => {
      if (view !== 'tasks') return
      void refreshTasks()
    }, [view, refreshTasks])

    // The trail of successful writes. It is only read when the tab is open,
    // so the other roles never ask for it.
    const refreshAuditLogs = useCallback(async () => {
      if (!canEdit) {
        setAuditLoading(false)
        return
      }
      setAuditLoading(true)
      try {
        const data = await api.listAuditLogs({
          limit: AUDIT_PAGE_SIZE,
          offset: auditOffset,
          action: auditAction,
          q: auditQuery,
        })
        setAuditLogs(data.logs)
        setAuditTotal(data.total)
        setAuditError(null)
      } catch (err) {
        setAuditError(err instanceof Error ? err.message : 'Could not load the activity log.')
      } finally {
        setAuditLoading(false)
      }
    }, [canEdit, auditOffset, auditAction, auditQuery])

    // The trail is loaded when the tab opens, and again whenever the filter
    // changes. A change made in another tab shows up on the next visit.
    useEffect(() => {
      if (view !== 'activity') return
      void refreshAuditLogs()
    }, [view, refreshAuditLogs])

    // The operational report of a period. The server counts the numbers, so
    // the browser only draws them; the report is asked for again whenever the
    // period or the unit changes.
    const reportParams = useMemo(
      () => ({
        from: reportFrom,
        to: reportTo,
        vehicle_id: reportVehicle === '' ? null : Number(reportVehicle),
      }),
      [reportFrom, reportTo, reportVehicle],
    )

    const refreshReport = useCallback(async () => {
      setReportLoading(true)
      try {
        setReport(await api.getReport(reportParams))
        setReportError(null)
      } catch (err) {
        setReportError(err instanceof Error ? err.message : 'Could not load the report.')
      } finally {
        setReportLoading(false)
      }
    }, [reportParams])

    useEffect(() => {
      if (view !== 'reports') return
      void refreshReport()
    }, [view, refreshReport])

    /** Pick one of the periods of the report. */
    const changeReportPreset = useCallback((preset: ReportPreset) => {
      setReportPreset(preset)
      if (preset === 'today') {
        setReportFrom(reportDay(0))
        setReportTo(reportDay(0))
      } else if (preset === 'week') {
        setReportFrom(reportDay(-6))
        setReportTo(reportDay(0))
      } else if (preset === 'month') {
        setReportFrom(reportDay(-29))
        setReportTo(reportDay(0))
      }
    }, [])

    /** A day of our own: the period is no longer one of the presets. */
    const changeReportFrom = useCallback((day: string) => {
      setReportPreset('custom')
      setReportFrom(day)
    }, [])

    const changeReportTo = useCallback((day: string) => {
      setReportPreset('custom')
      setReportTo(day)
    }, [])

    /**
     * Download the report as a CSV file.
     *
     * The file is fetched with the token of the session, so the server knows
     * who is asking; a plain link would not carry it.
     */
    const exportReport = useCallback(async () => {
      setReportExporting(true)
      try {
        const { blob, filename } = await api.downloadReportCSV(reportParams)
        const url = URL.createObjectURL(blob)
        const link = document.createElement('a')
        link.href = url
        link.download = filename
        document.body.appendChild(link)
        link.click()
        link.remove()
        URL.revokeObjectURL(url)
        showToast('success', `Report downloaded: ${filename}`)
      } catch (err) {
        showToast('error', err instanceof Error ? err.message : 'Could not export the report.')
      } finally {
        setReportExporting(false)
      }
    }, [reportParams, showToast])

    /** Narrow the trail down to one kind of action. */
    const setAuditActionFilter = useCallback((action: string) => {
      setAuditAction(action)
      setAuditOffset(0)
    }, [])

    /** Search the trail for a user or a message. */
    const setAuditSearch = useCallback((query: string) => {
      setAuditQuery(query)
      setAuditOffset(0)
    }, [])

    /** Driver accounts a unit can be handed over to. */
    const drivers = useMemo(() => accounts.filter((u) => u.role === 'driver'), [accounts])

    /**
     * The accounts a work order can be handed to: the drivers on the road and
     * the staff in the field. The admin and the viewer do not do the work, so
     * they are not on the list.
     */
    const taskAssignees = useMemo(
      () => accounts.filter((u) => u.role === 'driver' || u.role === 'staff'),
      [accounts],
    )

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
              setToast({ kind: 'success', text: 'Delivery complete — route closed.', id: Date.now() })
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
              // A unit crossed a zone boundary. The crossing is written to
              // the history of the live panel, where it stays until the
              // operator scrolls it away.
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
              // The aggregate of the day counts this crossing too, so the
              // Overview tab is asked for fresh numbers.
              void refreshOverview()
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
      [applyZoneCounts, refreshOverview],
    )

    // Drop the selection when a unit leaves the fleet.
    useEffect(() => {
      if (selectedVehicleId == null) return
      if (!vehicles.some((v) => v.id === selectedVehicleId)) setSelectedVehicleId(null)
    }, [vehicles, selectedVehicleId])

    // Half-picked stops belong to one vehicle: switching units or closing the
    // picker must not leave a stray A/B marker on the map.
    useEffect(() => {
      setRouteDraft({ origin: null, stops: [] })
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

    const handleRoutePickStart = (pick: RoutePick) => {
      // One map mode at a time: arming a route stop disarms entity pinning.
      setPicking(false)
      setRoutePick(pick)
    }

    /**
     * Plan a route and draw it. The server answers with the road path
     * (OSRM) or the straight line when no engine could be reached, and the
     * camera moves to the new line so it is never drawn off screen.
     *
     * The server also decides the order the stops are visited in, so the
     * answer carries the stops in that order: the map numbers them.
     */
    const planRoute = useCallback(
      async (vehicleId: number, origin: RouteStop, stops: RouteStop[]) => {
        setRouteSaving(true)
        try {
          const route = await api.setVehicleRoute(vehicleId, { origin, destinations: stops })
          setRoutes((prev) => mergeRoutes(prev, [route]))
          setRouteDraft({ origin: null, stops: [] })
          setRoutePick(null)
          setRouteFocus((f) => ({ id: route.id, n: (f?.n ?? 0) + 1 }))
          const last = route.stops?.length ? route.stops[route.stops.length - 1] : route.destination
          showToast(
            'success',
            `Route planned to ${last?.name || 'the destination'} · ${formatDistance(route.distance_m)}`,
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
        setRouteDraft({ origin, stops: [] })
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
        name: labelForPoint(lat, lng, routePick.stage === 'origin' ? 'Origin' : 'Stop'),
      }
      setRouteDraft((prev) =>
        routePick.stage === 'origin'
          ? { ...prev, origin: stop }
          : {
              ...prev,
              stops: prev.stops.map((s, i) => (i === routePick.index ? stop : s)),
            },
      )
      setRoutePick(null)
    }

    const handleRouteLabel = (pick: RoutePick, name: string) => {
      setRouteDraft((prev) => {
        if (pick.stage === 'origin') {
          if (!prev.origin) return prev
          return { ...prev, origin: { ...prev.origin, name } }
        }
        const current = prev.stops[pick.index]
        if (!current) return prev
        return {
          ...prev,
          stops: prev.stops.map((s, i) => (i === pick.index ? { ...current, name } : s)),
        }
      })
    }

    /**
     * A new destination row, armed for picking on the map straight away.
     *
     * The index is read before the state is updated: arming the picker from
     * inside the updater would be a side effect during the render, and React
     * may run the updater more than once.
     */
    const handleRouteAddStop = useCallback(() => {
      const index = routeDraft.stops.length
      setRouteDraft((prev) => ({ ...prev, stops: [...prev.stops, null] }))
      setRoutePick({ stage: 'stop', index })
    }, [routeDraft.stops.length])

    /**
     * Drop a destination row. The rows after it move up, so the index of an
     * armed row has to follow.
     */
    const handleRouteRemoveStop = useCallback((index: number) => {
      setRouteDraft((prev) => ({
        ...prev,
        stops: prev.stops.filter((_, i) => i !== index),
      }))
      setRoutePick((pick) => {
        if (!pick || pick.stage !== 'stop') return pick
        if (pick.index === index) return null
        if (pick.index > index) return { stage: 'stop', index: pick.index - 1 }
        return pick
      })
    }, [])

    const handleRouteSet = async (vehicleId: number, origin: RouteStop, stops: RouteStop[]) => {
      if (!canEdit) return
      await planRoute(vehicleId, origin, stops)
    }

    const handleRouteClear = async (vehicleId: number) => {
      if (!canEdit) return
      await api.clearVehicleRoute(vehicleId)
      setRoutes((prev) => prev.filter((r) => r.vehicle_id !== vehicleId))
      setRouteDraft({ origin: null, stops: [] })
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

    // Work orders: the board of the field. An admin fills it, a driver or a
    // staff member moves the cards along.
    const handleTaskCreate = async (input: TaskInput) => {
      if (!canEdit) return
      const created = await api.createTask(input)
      setTasks((prev) => [created, ...prev])
      showToast('success', `"${created.title}" added to the board`)
    }

    const handleTaskUpdate = async (id: number, input: TaskInput) => {
      if (!canEdit) return
      const updated = await api.updateTask(id, input)
      setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)))
      showToast('success', `"${updated.title}" saved`)
    }

    const handleTaskDelete = async () => {
      if (!canEdit || !taskToDelete) return
      setTaskDeleting(true)
      try {
        await api.deleteTask(taskToDelete.id)
        setTasks((prev) => prev.filter((t) => t.id !== taskToDelete.id))
        setTaskToDelete(null)
        showToast('success', `"${taskToDelete.title}" removed`)
      } catch (err) {
        showToast('error', err instanceof Error ? err.message : 'Could not delete the task')
      } finally {
        setTaskDeleting(false)
      }
    }

    // Moving a card along is the work of the field: a driver closes their
    // own task, a staff member closes any task.
    const handleTaskStatus = async (id: number, status: TaskStatus) => {
      try {
        const updated = await api.updateTaskStatus(id, status)
        setTasks((prev) => prev.map((t) => (t.id === id ? updated : t)))
        showToast('success', `"${updated.title}" is now ${taskStatusLabel(status).toLowerCase()}`)
      } catch (err) {
        showToast('error', err instanceof Error ? err.message : 'Could not change the status')
        throw err
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

  // The summary row of the Overview tab: the aggregate of the day as the
  // server counted it (distance, fleet status, zone crossings).
  const overviewProps = {
    data: overview,
    loading: overviewLoading,
    error: overviewError,
    onRetry: () => void refreshOverview(),
  }

  const page = NAV.find((n) => n.key === view) ?? NAV[0]
  const navCount = (k: Tab): number | null =>
    k === 'entities'
      ? entities.length
      : k === 'zones'
        ? zones.length
        : k === 'vehicles'
          ? vehicles.length
          : k === 'tasks'
            ? tasks.length
            : k === 'users'
              ? accounts.length
              : null

  /** Tabs the signed-in role may open; the sidebar renders only these. */
  const visibleNav = useMemo(
    () => NAV.filter((n) => canEdit || !ADMIN_TABS.includes(n.key)),
    [canEdit],
  )

  /** Which way the page moves: forward along the tab order, or back. */
  const [prevView, setPrevView] = useState<Tab>(view)
  const [navDir, setNavDir] = useState<'fwd' | 'back'>('fwd')
  if (prevView !== view) {
    setPrevView(view)
    setNavDir(
      NAV.findIndex((n) => n.key === view) >= NAV.findIndex((n) => n.key === prevView) ? 'fwd' : 'back',
    )
  }

  /** Phone dock: four tabs in reach, the rest tucked behind "More". */
  const [moreOpen, setMoreOpen] = useState(false)
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 768px)')
    const close = () => mq.matches && setMoreOpen(false)
    mq.addEventListener('change', close)
    return () => mq.removeEventListener('change', close)
  }, [])
  const dockMain = useMemo(
    () => visibleNav.filter((n) => DOCK_MAIN.includes(n.key)),
    [visibleNav],
  )
  const dockExtra = useMemo(
    () => visibleNav.filter((n) => !DOCK_MAIN.includes(n.key)),
    [visibleNav],
  )
  const navIcon = (k: Tab) =>
    k === 'overview'
      ? IconDashboard
      : k === 'entities'
        ? IconPin
        : k === 'zones'
          ? IconPolygon
          : k === 'tasks'
            ? IconClipboard
            : k === 'reports'
              ? IconFile
              : k === 'users'
                ? IconUsers
                : k === 'activity'
                  ? IconHistory
                  : IconTruck

  useEffect(() => {
    setMoreOpen(false)
  }, [view])

  useEffect(() => {
    if (!moreOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMoreOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [moreOpen])

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
    <div className={`dash${railCollapsed ? ' rail-slim' : ''}`} data-view={view} data-dir={navDir}>
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
                    : n.key === 'tasks'
                      ? IconClipboard
                      : n.key === 'reports'
                        ? IconFile
                        : n.key === 'users'
                          ? IconUsers
                          : n.key === 'activity'
                            ? IconHistory
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
              <span className={`chip stream ${streamState}`} title="Live vehicle feed" aria-label={streamLabel}>
                <i aria-hidden="true" />
                <span className="chip-text">{streamLabel}</span>
              </span>
            )}
            <span className={`chip api ${apiUi.cls}`} title={apiUi.title}>
              <i aria-hidden="true" />
              <span className="chip-text">{apiUi.label}</span>
            </span>
            <NotificationBell onNotice={showToast} />
            <span className="topbar-mobile-tools">
              {themeBtn}
              {logoutBtn}
            </span>
          </div>
        </header>

        {/* ---- Stage -------------------------------------------------------- */}
        <main className="stage" data-view={view} data-tall={mapTall ? 'true' : 'false'} data-mpane={mobilePane}>
          {view !== 'overview' && view !== 'users' && view !== 'tasks' && view !== 'reports' && (
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
              <OverviewStatCards {...overviewProps} />
            </div>
          )}

          {view !== 'users' && view !== 'tasks' && view !== 'reports' && (
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

            {/* Playback of a recorded trip: the dialog floats over the map, so
                the operator can watch the ghost marker walk the track. */}
            {playback.vehicleId != null && (
              <PlaybackDialog
                vehicleName={playbackVehicle?.name ?? 'this unit'}
                date={playback.date}
                points={playback.data}
                index={playback.index}
                playing={playback.playing}
                speed={playback.speed}
                loading={playback.loading}
                error={playback.error}
                onDateChange={playback.setDate}
                onLoad={() => void playback.load()}
                onToggle={playback.toggle}
                onSeek={playback.seek}
                onSpeedChange={playback.setSpeed}
                onClose={playback.close}
              />
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
          <span>
            Click the map to set{' '}
            {routePick.stage === 'origin' ? 'the origin (A)' : `stop ${routePick.index + 1}`}
          </span>
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
                <OverviewActivityCard {...overviewProps} onOpenFleet={() => goTo('vehicles')} />
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
            {view === 'tasks' ? (
              <TaskBoard
                tasks={tasks}
                loading={tasksLoading}
                error={tasksError}
                places={entities}
                assignees={taskAssignees}
                canManage={canEdit}
                canMove={role !== 'viewer'}
                onRetry={() => void refreshTasks()}
                onCreate={handleTaskCreate}
                onUpdate={handleTaskUpdate}
                onDelete={setTaskToDelete}
                onStatus={handleTaskStatus}
              />
            ) : view === 'reports' ? (
              <ReportsPanel
                data={report}
                loading={reportLoading}
                error={reportError}
                vehicles={vehicles}
                vehicleId={reportVehicle}
                from={reportFrom}
                to={reportTo}
                preset={reportPreset}
                scoped={role === 'driver'}
                exporting={reportExporting}
                onPresetChange={changeReportPreset}
                onFromChange={changeReportFrom}
                onToChange={changeReportTo}
                onVehicleChange={setReportVehicle}
                onRetry={() => void refreshReport()}
                onExport={() => void exportReport()}
              />
            ) : view === 'users' && canEdit ? (
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
            ) : view === 'activity' && canEdit ? (
              <ActivityLogPanel
                logs={auditLogs}
                total={auditTotal}
                loading={auditLoading}
                error={auditError}
                action={auditAction}
                query={auditQuery}
                offset={auditOffset}
                onActionChange={setAuditActionFilter}
                onQueryChange={setAuditSearch}
                onOffsetChange={setAuditOffset}
                onRetry={() => void refreshAuditLogs()}
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
                onAddStop={handleRouteAddStop}
                onRemoveStop={handleRouteRemoveStop}
                routeBusy={routeSaving}
                onClearDraft={() => {
                  setRouteDraft({ origin: null, stops: [] })
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

              {!formOpen && view !== 'tasks' && (
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
        {[...dockMain, ...(dockExtra.length === 1 ? dockExtra : [])].map((n) => {
          const Icon = navIcon(n.key)
          const count = navCount(n.key)
          return (
            <button
              key={n.key}
              type="button"
              className={view === n.key ? 'active' : ''}
              aria-current={view === n.key ? 'page' : undefined}
              aria-label={n.label}
              onClick={() => {
                setMoreOpen(false)
                goTo(n.key)
              }}
            >
              <span className="bn-icon">
                <Icon width={22} height={22} />
                {count != null && count > 0 && (
                  <em className={n.key === 'vehicles' && streamState === 'live' ? 'live' : ''}>
                    {count > 99 ? '99+' : count}
                  </em>
                )}
              </span>
              <span className="bn-label">{n.label}</span>
            </button>
          )
        })}
        {dockExtra.length > 1 && (
          <button
            type="button"
            className={moreOpen || dockExtra.some((n) => n.key === view) ? 'active' : ''}
            aria-haspopup="dialog"
            aria-expanded={moreOpen}
            aria-label="More"
            onClick={() => setMoreOpen((open) => !open)}
          >
            <span className="bn-icon">
              <IconMore width={22} height={22} />
            </span>
            <span className="bn-label">More</span>
          </button>
        )}
      </nav>

      {moreOpen && (
        <div className="more-overlay" onClick={() => setMoreOpen(false)}>
          <div
            className="more-sheet"
            role="dialog"
            aria-modal="true"
            aria-label="More"
            onClick={(e) => e.stopPropagation()}
          >
            <span className="more-grip" aria-hidden="true" />
            <h2 className="more-title">More</h2>
            <ul className="more-list">
              {dockExtra.map((n) => {
                const Icon = navIcon(n.key)
                const count = navCount(n.key)
                return (
                  <li key={n.key}>
                    <button
                      type="button"
                      className={`more-item${view === n.key ? ' active' : ''}`}
                      aria-current={view === n.key ? 'page' : undefined}
                      onClick={() => {
                        setMoreOpen(false)
                        goTo(n.key)
                      }}
                    >
                      <span className="more-ico">
                        <Icon width={20} height={20} />
                      </span>
                      <span className="more-text">
                        <b>{n.label}</b>
                        <small>{n.sub}</small>
                      </span>
                      {count != null && <span className="more-count">{count}</span>}
                    </button>
                  </li>
                )
              })}
            </ul>
          </div>
        </div>
      )}

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

      {canEdit && taskToDelete && (
        <ConfirmDialog
          open
          title="Delete this task?"
          message={`"${taskToDelete.title}" will be removed for good.`}
          confirmLabel="Delete"
          busy={taskDeleting}
          onConfirm={handleTaskDelete}
          onCancel={() => setTaskToDelete(null)}
        />
      )}

      {toast && (
        <div
          key={toast.id}
          className={`toast ${toast.kind}`}
          role={toast.kind === 'success' ? 'status' : 'alert'}
        >
          <span className="toast-ico" aria-hidden="true">
            {toast.kind === 'success' ? <IconCheck width={18} height={18} /> : <IconAlert width={18} height={18} />}
          </span>
          <div className="toast-body">
            <em>{TOAST_LABEL[toast.kind]}</em>
            <span>{toast.text}</span>
          </div>
          <i className="toast-bar" aria-hidden="true" />
        </div>
      )}
    </div>
  )
}
