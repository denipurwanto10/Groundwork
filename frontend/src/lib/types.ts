export interface Entity {
  id: number
  name: string
  type: string
  status: 'active' | 'inactive'
  latitude: number
  longitude: number
  created_at: string
  updated_at: string
}

export interface EntityInput {
  name: string
  type: string
  status: 'active' | 'inactive' | ''
  latitude: number
  longitude: number
}

export type EntityFieldErrors = Partial<Record<'name' | 'type' | 'status' | 'latitude' | 'longitude', string>>

/** Mirrors backend validation (internal/entity) for instant feedback. */
export function validateEntityInput(input: EntityInput): EntityFieldErrors {
  const errors: EntityFieldErrors = {}

  if (!input.name.trim()) {
    errors.name = 'Name is required.'
  } else if (input.name.trim().length > 255) {
    errors.name = 'Name must be at most 255 characters.'
  }

  if (!input.type.trim()) {
    errors.type = 'Type is required.'
  } else if (input.type.trim().length > 100) {
    errors.type = 'Type must be at most 100 characters.'
  }

  if (input.status !== 'active' && input.status !== 'inactive') {
    errors.status = "Status must be 'active' or 'inactive'."
  }

  if (!Number.isFinite(input.latitude) || input.latitude < -90 || input.latitude > 90) {
    errors.latitude = 'Latitude must be between -90 and 90.'
  }

  if (!Number.isFinite(input.longitude) || input.longitude < -180 || input.longitude > 180) {
    errors.longitude = 'Longitude must be between -180 and 180.'
  }

  return errors
}

export const emptyEntityInput: EntityInput = {
  name: '',
  type: '',
  status: 'active',
  latitude: 0,
  longitude: 0,
}

/** Fixed type list (select options, English). Indonesian values from the old
 *  free-text field keep working as legacy aliases (see below). */
export const ENTITY_TYPES = [
  'destination',
  'monument',
  'historic',
  'park',
  'recreation',
  'zoo',
  'culinary',
  'lodging',
] as const

/** Distinct marker colour per type; legacy Indonesian values map to the same colour. */
export const TYPE_COLORS: Record<string, string> = {
  destination: '#c8161d',
  destinasi: '#c8161d',
  monument: '#d4535c',
  monumen: '#d4535c',
  historic: '#d49a3a',
  bersejarah: '#d49a3a',
  park: '#3a9d6e',
  taman: '#3a9d6e',
  recreation: '#8664c8',
  rekreasi: '#8664c8',
  zoo: '#e07b3c',
  'kebun binatang': '#e07b3c',
  culinary: '#cf5a95',
  kuliner: '#cf5a95',
  lodging: '#2b9aa8',
  penginapan: '#2b9aa8',
}

/** Legacy Indonesian values (old free-text field) map to the fixed list. */
export const LEGACY_TYPE_ALIASES: Record<string, string> = {
  destinasi: 'destination',
  monumen: 'monument',
  bersejarah: 'historic',
  taman: 'park',
  rekreasi: 'recreation',
  'kebun binatang': 'zoo',
  kuliner: 'culinary',
  penginapan: 'lodging',
}

/** Canonical form for the select: legacy alias → fixed value, else as-is. */
export function canonicalType(type: string): string {
  const s = type.trim().toLowerCase()
  return LEGACY_TYPE_ALIASES[s] ?? s
}

/** Stable colour for any type: fixed map first, hash fallback for unknowns. */
export function typeColorFor(type: string): string {
  const s = type.trim().toLowerCase()
  if (!s) return '#7d8693'
  const fixed = TYPE_COLORS[s]
  if (fixed) return fixed
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360
  return `hsl(${h} 42% 46%)`
}

/* ------------------------------------------------------------------ *
 * Operator-drawn zones (migration 005).
 * Official boundaries live in admin_regions and are read-only; these
 * are the editable/deletable polygons the operator draws on the map.
 * ------------------------------------------------------------------ */

/** Mirrors the CHECK constraint on zones.category. */
export const ZONE_CATEGORIES = ['operational', 'restricted', 'planned', 'other'] as const
export type ZoneCategory = (typeof ZONE_CATEGORIES)[number]

export const ZONE_CATEGORY_LABELS: Record<ZoneCategory, string> = {
  operational: 'Operational',
  restricted: 'Restricted',
  planned: 'Planned',
  other: 'Other',
}

/** Suggested colours per category — the picker still accepts any hex. */
export const ZONE_CATEGORY_COLORS: Record<ZoneCategory, string> = {
  operational: '#c8161d',
  restricted: '#7a1a1f',
  planned: '#d49a3a',
  other: '#7d8693',
}

export const DEFAULT_ZONE_COLOR = '#c8161d'

export type ZoneGeometryType = 'Polygon' | 'MultiPolygon'

export interface ZoneGeometry {
  type: ZoneGeometryType
  /** GeoJSON ring nesting; PostGIS parses it, we never index into it blindly. */
  coordinates: unknown
}

/** Omitted geometry on update means "keep the stored shape". */
export interface ZoneInput {
  name: string
  category: ZoneCategory
  color: string
  description: string
  region_level: number | null
  region_code: string | null
  geometry?: ZoneGeometry | null
}

/** Properties as returned by the backend (derived measurements included). */
export interface ZoneProperties {
  id: number
  name: string
  category: ZoneCategory
  color: string
  description: string
  region_level: number | null
  region_code: string | null
  region_name: string | null
  /** Server-computed geodesic area, km². */
  area_km2: number
  /** Server-computed geodesic perimeter, km. */
  perimeter_km: number
  vertices: number
  /** Entities whose point falls inside this zone (server-side ST_Covers). */
  entity_count?: number
  /** Vehicles whose last reported position falls inside this zone. */
  vehicle_count?: number
  created_at: string
  updated_at: string
}

export interface ZoneFeature {
  type: 'Feature'
  id: number
  properties: ZoneProperties
  geometry: ZoneGeometry | null
}

export interface ZoneCollection {
  type: 'FeatureCollection'
  features: ZoneFeature[]
}

export interface ZoneImportReport {
  imported: number
  failed: { index: number; name: string; reason: string }[]
  zones: ZoneFeature[]
}

export type ZoneFieldErrors = Partial<
  Record<'name' | 'category' | 'color' | 'description' | 'region_code' | 'geometry', string>
>

const HEX_COLOR = /^#[0-9a-fA-F]{6}$/

/** Mirrors backend validation (internal/handler/zones.go) for instant feedback. */
export function validateZoneInput(input: ZoneInput): ZoneFieldErrors {
  const errors: ZoneFieldErrors = {}

  const name = input.name.trim()
  if (!name) {
    errors.name = 'Name is required.'
  } else if (name.length > 120) {
    errors.name = 'Name must be at most 120 characters.'
  }

  if (!ZONE_CATEGORIES.includes(input.category)) {
    errors.category = 'Pick a category.'
  }

  if (!HEX_COLOR.test(input.color)) {
    errors.color = 'Colour must be a hex value like #c8161d.'
  }

  if (input.description.trim().length > 500) {
    errors.description = 'Description must be at most 500 characters.'
  }

  if (input.region_code != null && input.region_code !== '') {
    const len = input.region_code.trim().length
    if (![2, 4, 6, 9, 12].includes(len)) {
      errors.region_code = 'Region code must be 2, 4, 6, 9 or 12 characters.'
    }
  }

  if (!input.geometry) {
    errors.geometry = 'Draw a polygon on the map first.'
  } else if (input.geometry.type !== 'Polygon' && input.geometry.type !== 'MultiPolygon') {
    errors.geometry = 'Geometry must be a Polygon or MultiPolygon.'
  }

  return errors
}

export const emptyZoneInput: ZoneInput = {
  name: '',
  category: 'operational',
  color: DEFAULT_ZONE_COLOR,
  description: '',
  region_level: null,
  region_code: null,
  geometry: null,
}

/* ------------------------------------------------------------------ *
 * Official administrative regions (migration 003 / HDX cod-ab-idn).
 * Read-only: imported from BPS, never edited in the app.
 * ------------------------------------------------------------------ */

export const REGION_LEVELS = [
  { level: 0, label: 'Country', plural: 'Country' },
  { level: 1, label: 'Province', plural: 'Provinces' },
  { level: 2, label: 'Regency / city', plural: 'Regencies & cities' },
  { level: 3, label: 'District', plural: 'Districts' },
  { level: 4, label: 'Village', plural: 'Villages' },
] as const

export interface RegionProperties {
  code: string
  name: string
  level: number
  parent_code: string | null
  source?: string
  license?: string
  source_version?: string
}

export interface RegionFeature {
  type: 'Feature'
  properties: RegionProperties
  geometry: ZoneGeometry | null
}

export interface RegionCollection {
  type: 'FeatureCollection'
  features: RegionFeature[]
}

export interface RegionCount {
  level: number
  count: number
}

/** Region code → level, using the BPS concatenated widths (2/4/6/9/12). */
export function levelFromRegionCode(code: string | null | undefined): number | null {
  if (!code) return null
  const table: Record<number, number> = { 2: 0, 4: 1, 6: 2, 9: 3, 12: 4 }
  return table[code.trim().length] ?? null
}

/* ------------------------------------------------------------------ *
 * Tracked vehicles (migration 006).
 * Live tracking: the REST list is the starting state, then every
 * accepted position arrives over /api/vehicles/stream.
 * ------------------------------------------------------------------ */

/** Mirrors the CHECK constraint on vehicles.kind. */
export const VEHICLE_KINDS = ['truck', 'van', 'motorcycle', 'pickup'] as const
export type VehicleKind = (typeof VEHICLE_KINDS)[number]

export const VEHICLE_KIND_LABELS: Record<VehicleKind, string> = {
  truck: 'Truck',
  van: 'Van',
  motorcycle: 'Motorcycle',
  pickup: 'Pickup',
}

/** Mirrors the CHECK constraint on vehicles.status. */
export const VEHICLE_STATUSES = ['idle', 'moving', 'offline'] as const
export type VehicleStatus = (typeof VEHICLE_STATUSES)[number]

export const VEHICLE_STATUS_LABELS: Record<VehicleStatus, string> = {
  idle: 'Idle',
  moving: 'Moving',
  offline: 'Offline',
}

/** Marker colour follows the status: motion first, then parked, then stale. */
export const VEHICLE_STATUS_COLORS: Record<VehicleStatus, string> = {
  moving: '#d0191f',
  idle: '#d49a3a',
  offline: '#7d8693',
}

/** The colour a unit's delivery line gets when nobody picked one. */
export const DEFAULT_ROUTE_COLOR = '#d0171e'

/** One tracked unit, latest known position included (null until it reports). */
export interface Vehicle {
  id: number
  code: string
  name: string
  kind: VehicleKind
  status: VehicleStatus
  /** The driver this unit is handed over to; null means unassigned. */
  driver_id: number | null
  lat: number | null
  lng: number | null
  speed_kmh: number
  /**
   * Compass bearing the unit is facing, in degrees (0 = north, 90 = east).
   * Null until the feed reports one; the marker then follows the direction
   * between the last two positions.
   */
  heading: number | null
  /** Colour of this unit's delivery line. */
  route_color: string
  last_seen_at: string | null
  /** The delivery route the unit is following, if any. */
  active_route?: VehicleRoute | null
}

/** Body of the create/update vehicle calls. */
export interface VehicleInput {
  code: string
  name: string
  kind: VehicleKind
  driver_id: number | null
  /** Colour of the delivery line of this unit. */
  route_color: string
}

/** The counters of one zone: how many places and units are inside it. */
export interface ZoneCount {
  id: number
  entity_count: number
  vehicle_count: number
}

/** One account without its credentials: the fleet form assigns a driver. */
export interface UserAccount {
  id: number
  username: string
  email: string
  name: string
  /** 'male', 'female', or '' when the account has no gender set. */
  gender: string
  role: string
  created_at?: string
}

/** What the admin account form sends when registering or editing a user. */
export interface UserInput {
  username: string
  email: string
  password: string
  name: string
  gender: string
  role: string
}

export interface VehicleList {
  vehicles: Vehicle[]
  count: number
}

/** Frames pushed over /api/vehicles/stream (see internal/handler/vehicles.go). */
export type VehicleFrame =
  | { type: 'snapshot'; vehicles: Vehicle[]; at: string }
  | { type: 'position'; vehicle: Vehicle; at: string }
  | { type: 'vehicle_updated'; vehicle: Vehicle; at: string }
  | { type: 'vehicle_deleted'; id: number; at: string }
  | { type: 'route'; route: VehicleRoute; at: string }
  | { type: 'route_cleared'; route_id: number; vehicle_id: number; at: string }
  | { type: 'route_completed'; route_id: number; vehicle_id: number; at: string }
  | { type: 'zones'; zones: ZoneCount[]; at: string }
  | {
      type: 'geofence'
      id: number
      event: GeofenceEventType
      vehicle_id: number
      vehicle_name: string
      zone_id: number
      zone_name: string
      timestamp: string
      at: string
    }

/** What a sign-in hands out: the access token, the refresh token and the account. */
export interface LoginResult {
  token: string
  refresh_token: string
  expires_in: number
  user: UserAccount
}

/** Whether the workspace still needs its first account. */
export interface AuthStatus {
  users: number
  setup_required: boolean
}

/** Which way a vehicle crossed a zone boundary. */
export type GeofenceEventType = 'ENTER' | 'EXIT'

/** One crossing of a zone boundary, as stored and as pushed over the feed. */
export interface GeofenceEvent {
  id: number
  vehicle_id: number
  vehicle_name: string
  zone_id: number
  zone_name: string
  event_type: GeofenceEventType
  created_at: string
}

/* ------------------------------------------------------------------ *
 * Work orders (migration 021): the job a unit is sent out for.
 * ------------------------------------------------------------------ */

/** The three steps a work order walks through. */
export const TASK_STATUSES = ['pending', 'in_progress', 'completed'] as const
export type TaskStatus = (typeof TASK_STATUSES)[number]

export const TASK_STATUS_LABELS: Record<TaskStatus, string> = {
  pending: 'Pending',
  in_progress: 'In Progress',
  completed: 'Done',
}

/** One work order, as the board shows it. */
export interface Task {
  id: number
  title: string
  description: string
  status: TaskStatus
  /** The account that has to do the work; null means nobody is assigned. */
  assigned_user_id: number | null
  /** The place the work is about; null means no destination was picked. */
  place_id: number | null
  /** The day the work has to be done by (YYYY-MM-DD), or null. */
  due_date: string | null
  created_at: string
  /** The name of the assigned account; empty when the account is gone. */
  assigned_name: string
  /** The name of the place; empty when the place is gone. */
  place_name: string
}

/** What the task form sends when a task is created or edited. */
export interface TaskInput {
  title: string
  description: string
  status: TaskStatus
  assigned_user_id: number | null
  place_id: number | null
  /** The deadline as YYYY-MM-DD; empty means no deadline. */
  due_date: string
}

export type TaskFieldErrors = Partial<
  Record<'title' | 'description' | 'status' | 'assigned_user_id' | 'place_id' | 'due_date', string>
>

/** Mirrors backend validation (internal/task) for instant feedback. */
export function validateTaskInput(input: TaskInput): TaskFieldErrors {
  const errors: TaskFieldErrors = {}

  const title = input.title.trim()
  if (!title) {
    errors.title = 'Title is required.'
  } else if (title.length > 160) {
    errors.title = 'Title must be at most 160 characters.'
  }

  if (input.description.trim().length > 2000) {
    errors.description = 'Description must be at most 2000 characters.'
  }

  if (!TASK_STATUSES.includes(input.status)) {
    errors.status = 'Status must be pending, in_progress or completed.'
  }

  if (input.due_date && !/^\d{4}-\d{2}-\d{2}$/.test(input.due_date)) {
    errors.due_date = 'Deadline must be a date like 2026-10-25.'
  }

  return errors
}

/** Values come from the wire, so coerce anything unexpected to a known one. */
export function normalizeTaskStatus(status: string): TaskStatus {
  return (TASK_STATUSES as readonly string[]).includes(status) ? (status as TaskStatus) : 'pending'
}

export function taskStatusLabel(status: string): string {
  return TASK_STATUS_LABELS[normalizeTaskStatus(status)]
}

/**
 * "Overdue" / "Due today" / "Due in 3 days" — how the deadline reads on a
 * card. A task without a deadline has none.
 */
export function formatTaskDue(due: string | null, now = Date.now()): string {
  if (!due) return 'No deadline'
  const target = Date.parse(`${due}T00:00:00`)
  if (Number.isNaN(target)) return due
  const today = new Date(now)
  today.setHours(0, 0, 0, 0)
  const days = Math.round((target - today.getTime()) / 86_400_000)
  if (days < 0) return `Overdue by ${Math.abs(days)}d`
  if (days === 0) return 'Due today'
  if (days === 1) return 'Due tomorrow'
  return `Due in ${days}d`
}

/** True when the deadline of a task is today or in the past. */
export function isTaskOverdue(due: string | null, now = Date.now()): boolean {
  if (!due) return false
  const target = Date.parse(`${due}T23:59:59`)
  if (Number.isNaN(target)) return false
  return target < now
}

/* ------------------------------------------------------------------ *
 * Audit log (GET /api/audit-logs): who changed what, and when.
 * ------------------------------------------------------------------ */

/** What happened to an object. Mirrors the actions the backend writes. */
export type AuditAction = 'create' | 'update' | 'delete' | 'import' | 'set' | 'clear'

/** The kind of object an action was about. */
export type AuditEntityType = 'entity' | 'zone' | 'vehicle' | 'user' | 'route'

/** One recorded action of the trail. */
export interface AuditLog {
  id: number
  /** The account that made the request; null once the account is deleted. */
  user_id: number | null
  /** The identity of that account (its address, or its handle). */
  user_email: string
  action: AuditAction
  entity_type: AuditEntityType
  /** The object itself, when the request named one. */
  entity_id: number | null
  /** The sentence the operator reads: 'created zone "Gudang Utama"'. */
  details: string
  created_at: string
}

/** One page of the trail, plus the total number of entries behind it. */
export interface AuditLogList {
  logs: AuditLog[]
  /** How many rows this page holds. */
  count: number
  /** How many rows the trail holds in total. */
  total: number
}

/* ------------------------------------------------------------------ *
 * Overview (GET /api/overview): the aggregate of the current day.
 * ------------------------------------------------------------------ */

/** The fleet counted by its current status. */
export interface OverviewVehicles {
  total: number
  moving: number
  idle: number
  offline: number
}

/** One work area and how often it was crossed today. */
export interface OverviewZone {
  zone_id: number
  zone_name: string
  /** Crossings into the zone. */
  enter: number
  /** Crossings out of the zone. */
  exit: number
  total: number
}

/** The zone crossings of today, counted and broken down per work area. */
export interface OverviewEvents {
  total: number
  /** Crossings into a zone. */
  enter: number
  /** Crossings out of a zone. */
  exit: number
  zones: OverviewZone[]
}

/** The aggregate of the day behind the Overview tab. */
export interface Overview {
  /** The day the numbers belong to (YYYY-MM-DD, server time zone). */
  date: string
  /** How far the whole fleet drove today, in kilometres. */
  distance_km: number
  vehicles: OverviewVehicles
  events: OverviewEvents
  /** The latest zone crossings, newest first. */
  recent: GeofenceEvent[]
}

/** "2026-10-22" -> "22 Oct 2026": the day the overview numbers belong to. */
export function formatOverviewDate(date: string): string {
  if (!date) return ''
  const t = Date.parse(`${date}T00:00:00`)
  if (Number.isNaN(t)) return date
  return new Date(t).toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  })
}

/* ------------------------------------------------------------------ *
 * Reports (GET /api/reports): the operational report of a period.
 * ------------------------------------------------------------------ */

/** The window a report covers. */
export interface ReportPeriod {
  /** First day of the period (YYYY-MM-DD). */
  from: string
  /** Last day of the period (YYYY-MM-DD). */
  to: string
}

/** The numbers of the whole period. */
export interface ReportTotals {
  /** How far the whole fleet drove in the period. */
  distance_km: number
  /** The routes that were finished in the period. */
  routes_completed: number
  /** The routes that ran in the period, finished or not. */
  routes_total: number
  /** The work orders of the period that are done. */
  tasks_completed: number
  /** The work orders of the period. */
  tasks_total: number
  /** The crossings into a zone. */
  geofence_enter: number
  /** The crossings out of a zone. */
  geofence_exit: number
}

/** One unit and what it did in the period. */
export interface ReportVehicle {
  vehicle_id: number
  vehicle_code: string
  vehicle_name: string
  /** The account of the driver; empty when the unit has none. */
  driver_name: string
  distance_km: number
  routes_completed: number
  routes_total: number
  geofence_enter: number
  geofence_exit: number
}

/** One work area and how often it was crossed in the period. */
export interface ReportZone {
  zone_id: number
  zone_name: string
  /** Crossings into the zone. */
  enter: number
  /** Crossings out of the zone. */
  exit: number
  /** Crossings of the border, in and out. */
  total: number
  /** How many units crossed the border. */
  unique_vehicles: number
}

/** The report of a period: the totals and the two breakdowns. */
export interface Report {
  period: ReportPeriod
  totals: ReportTotals
  by_vehicle: ReportVehicle[]
  by_zone: ReportZone[]
}

/** What a report request asks for. */
export interface ReportParams {
  /** First day of the period (YYYY-MM-DD). */
  from?: string
  /** Last day of the period (YYYY-MM-DD). */
  to?: string
  /** One unit only; null asks for the whole fleet. */
  vehicle_id?: number | null
}

/** "2026-10-22" -> "22 Oct 2026": the day a report number belongs to. */
export function formatReportDate(date: string): string {
  if (!date) return ''
  const t = Date.parse(`${date}T00:00:00`)
  if (Number.isNaN(t)) return date
  return new Date(t).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}

/** A unit without a reported position must not get a marker. */
export function hasPosition(v: Vehicle): v is Vehicle & { lat: number; lng: number } {
  return typeof v.lat === 'number' && typeof v.lng === 'number' && Number.isFinite(v.lat) && Number.isFinite(v.lng)
}

/**
 * Compass bearing from one point to another, in degrees (0 = north, 90 = east).
 * This is the direction a unit that moved from a to b is facing.
 */
export function bearingBetween(
  a: { lat: number; lng: number },
  b: { lat: number; lng: number },
): number {
  const r = (d: number) => (d * Math.PI) / 180
  const y = Math.sin(r(b.lng - a.lng)) * Math.cos(r(b.lat))
  const x =
    Math.cos(r(a.lat)) * Math.sin(r(b.lat)) -
    Math.sin(r(a.lat)) * Math.cos(r(b.lat)) * Math.cos(r(b.lng - a.lng))
  const deg = (Math.atan2(y, x) * 180) / Math.PI
  return (deg + 360) % 360
}

/** Values come from the wire, so coerce anything unexpected to a known one. */
export function normalizeVehicleKind(kind: string): VehicleKind {
  return (VEHICLE_KINDS as readonly string[]).includes(kind) ? (kind as VehicleKind) : 'truck'
}

export function normalizeVehicleStatus(status: string): VehicleStatus {
  return (VEHICLE_STATUSES as readonly string[]).includes(status) ? (status as VehicleStatus) : 'offline'
}

export function vehicleKindLabel(kind: string): string {
  return VEHICLE_KIND_LABELS[normalizeVehicleKind(kind)]
}

export function vehicleStatusLabel(status: string): string {
  return VEHICLE_STATUS_LABELS[normalizeVehicleStatus(status)]
}

export function vehicleStatusColor(status: string): string {
  return VEHICLE_STATUS_COLORS[normalizeVehicleStatus(status)]
}

/** Human age of the last reported position, e.g. "12s ago". */
export function formatLastSeen(iso: string | null, now = Date.now()): string {
  if (!iso) return 'no position yet'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return 'unknown'
  const secs = Math.max(0, Math.round((now - t) / 1000))
  if (secs < 10) return 'just now'
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

/** One recorded position of a unit, as the history endpoint returns it. */
export interface HistoryPoint {
  lat: number
  lng: number
  /** Speed in km/h. */
  speed: number
  /** Compass bearing in degrees, when the unit reported one. */
  heading: number | null
  /** RFC3339 timestamp of the report. */
  timestamp: string
}

/**
 * Speeds the playback can run at. 1x replays the whole track in about forty
 * seconds, whatever the number of points.
 */
export const PLAYBACK_SPEEDS = [0.5, 1, 2, 4, 8] as const
export type PlaybackSpeed = (typeof PLAYBACK_SPEEDS)[number]

/* ------------------------------------------------------------------ *
 * Delivery routes (migration 007).
 * A route is the planned from -> to path of one vehicle: what the map
 * draws as "jalur pengiriman", with the vehicle marker moving along it.
 * ------------------------------------------------------------------ */

export const ROUTE_STATUSES = ['active', 'completed', 'cancelled'] as const
export type RouteStatus = (typeof ROUTE_STATUSES)[number]

/** One end of a route. The label is what the operator typed (or picked). */
export interface RouteStop {
  lat: number
  lng: number
  name: string
}

/**
 * The stops of a route that is being planned.
 *
 * The origin is where the trip starts; the stops are the destinations, in the
 * order the operator picked them. A stop that is not picked yet is null: the
 * form shows the row and waits for the map click.
 */
export interface RouteDraft {
  origin: RouteStop | null
  stops: (RouteStop | null)[]
}

/**
 * Which stop of the draft the next map click fills.
 *
 * The origin is picked on its own; a stop is picked by its index in the
 * draft, so the operator can go back and move the third stop.
 */
export type RoutePick = { stage: 'origin' } | { stage: 'stop'; index: number }

/** GeoJSON LineString straight from PostGIS. Coordinates are [lng, lat]. */
export interface RouteGeometry {
  type: 'LineString'
  coordinates: [number, number][]
}

/** 'osrm' is a real driving path, 'direct' the straight-line fallback. */
export type RouteSource = 'osrm' | 'direct'

export interface VehicleRoute {
  id: number
  vehicle_id: number
  origin: RouteStop
  destination: RouteStop
  /**
   * The destinations of a multi-stop route, in the visiting order the server
   * worked out. The last one is the destination above. It is empty for a
   * route with a single destination.
   */
  stops?: RouteStop[]
  geometry: RouteGeometry
  distance_m: number
  duration_s: number
  source: RouteSource
  status: RouteStatus
  /** 0..1: how much of the line the vehicle's last position has covered. */
  progress: number
  remaining_m: number
  created_at: string
  ended_at: string | null
  /**
   * When the driver started the trip. A route without it is a plan waiting
   * for the driver.
   */
  started_at: string | null
  /**
   * True when the unit has a driver. The route of a unit without one is
   * driven right away; the route of a unit with a driver waits for the
   * driver's start.
   */
  driver_assigned: boolean
}

export interface RouteList {
  routes: VehicleRoute[]
  count: number
}

/**
 * An island group is not an administrative level: it is the union of the whole
 * provinces that belong to one island, grouped by their BPS code prefix.
 */
export interface IslandGroup {
  key: string
  label: string
  /** How many provinces the group actually has in the imported dataset. */
  provinces: number
  codes: string[]
}

export interface IslandList {
  islands: IslandGroup[]
  count: number
}

export function normalizeRouteSource(source: string): RouteSource {
  return source === 'osrm' ? 'osrm' : 'direct'
}

/** Leaflet wants [lat, lng]; GeoJSON stores [lng, lat]. */
export function routeLatLngs(route: VehicleRoute): [number, number][] {
  const coords = route.geometry?.coordinates ?? []
  return coords
    .filter((pair) => Array.isArray(pair) && pair.length >= 2 && Number.isFinite(pair[0]) && Number.isFinite(pair[1]))
    .map((pair) => [pair[1], pair[0]] as [number, number])
}

export function routeProgress(route: VehicleRoute): number {
  if (!Number.isFinite(route.progress)) return 0
  return Math.min(1, Math.max(0, route.progress))
}

/**
 * Seconds left at the unit's current speed. A parked unit (0 km/h) falls back to
 * the planned duration for the part of the line it has not covered, so the ETA
 * never reads as an infinite wait while the truck sits at a red light.
 */
export function routeEtaSeconds(route: VehicleRoute, speedKmh: number): number {
  const left = Math.max(0, route.remaining_m)
  if (speedKmh > 1) return left / (speedKmh / 3.6)
  return route.duration_s * (1 - routeProgress(route))
}

/**
 * The moment the unit is expected to reach the destination: the current clock
 * plus the time still to drive. It is the same estimate as routeEtaSeconds,
 * only as a wall-clock timestamp, so the two never disagree.
 */
export function routeEtaAt(route: VehicleRoute, speedKmh: number, now: number): number {
  return now + routeEtaSeconds(route, speedKmh) * 1000
}

/**
 * "14:32" — the wall clock the unit is expected to arrive. A trip that ends
 * after midnight carries the day offset, so "23:50 plus 3 h" does not read as
 * this morning.
 */
export function formatEtaClock(timestamp: number, now: number): string {
  if (!Number.isFinite(timestamp)) return '—'
  const at = new Date(timestamp)
  const clock = `${String(at.getHours()).padStart(2, '0')}:${String(at.getMinutes()).padStart(2, '0')}`

  // Calendar days, not 24-hour blocks: a trip that ends tomorrow at 01:00
  // crosses one local midnight, not a day and a bit.
  const from = new Date(now)
  from.setHours(0, 0, 0, 0)
  const to = new Date(at)
  to.setHours(0, 0, 0, 0)
  const days = Math.round((to.getTime() - from.getTime()) / 86_400_000)
  if (days > 0) return `${clock} +${days}d`
  if (days < 0) return `${clock} ${days}d`

  return clock
}

/** "640 m" / "12.4 km" — one decimal only once kilometres are reached. */
export function formatDistance(meters: number): string {
  if (!Number.isFinite(meters) || meters < 0) return '—'
  if (meters < 1000) return `${Math.round(meters)} m`
  return `${(meters / 1000).toFixed(meters < 10000 ? 1 : 0)} km`
}

/** "8 min" / "1 j 05 m" for the planned driving time. */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const mins = Math.round(seconds / 60)
  if (mins < 1) return '<1 min'
  if (mins < 60) return `${mins} min`
  const hours = Math.floor(mins / 60)
  const rest = mins % 60
  return rest === 0 ? `${hours} h` : `${hours} h ${rest} min`
}

/** Route for one vehicle, if it has one. */
export function routeForVehicle(routes: VehicleRoute[], vehicleId: number | null): VehicleRoute | undefined {
  if (vehicleId == null) return undefined
  return routes.find((r) => r.vehicle_id === vehicleId && r.status === 'active')
}

/**
 * Merge route lists into one, newest wins. The same route arrives from
 * several places — the fleet list carries the active route of every unit,
 * the route list returns all of them and the live feed pushes the progress
 * — so the map keeps one line per route id.
 */
export function mergeRoutes(current: VehicleRoute[], incoming: VehicleRoute[]): VehicleRoute[] {
  const byId = new Map<number, VehicleRoute>()
  for (const r of current) byId.set(r.id, r)
  for (const r of incoming) byId.set(r.id, r)
  return [...byId.values()].filter((r) => r.status === 'active')
}

/** The routes of the units in a fleet list. */
export function activeRoutesOf(vehicles: Vehicle[]): VehicleRoute[] {
  return vehicles
    .map((v) => v.active_route)
    .filter((r): r is VehicleRoute => !!r && r.status === 'active')
}
