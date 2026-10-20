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
  destination: '#2563eb',
  destinasi: '#2563eb',
  monument: '#dc2626',
  monumen: '#dc2626',
  historic: '#b45309',
  bersejarah: '#b45309',
  park: '#16a34a',
  taman: '#16a34a',
  recreation: '#9333ea',
  rekreasi: '#9333ea',
  zoo: '#ea580c',
  'kebun binatang': '#ea580c',
  culinary: '#db2777',
  kuliner: '#db2777',
  lodging: '#0891b2',
  penginapan: '#0891b2',
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
  if (!s) return '#64748b'
  const fixed = TYPE_COLORS[s]
  if (fixed) return fixed
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360
  return `hsl(${h} 65% 45%)`
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
  operational: '#2563eb',
  restricted: '#dc2626',
  planned: '#b45309',
  other: '#64748b',
}

export const DEFAULT_ZONE_COLOR = '#2563eb'

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
    errors.color = 'Colour must be a hex value like #2563eb.'
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
  moving: '#16a34a',
  idle: '#b45309',
  offline: '#64748b',
}

/** One tracked unit, latest known position included (null until it reports). */
export interface Vehicle {
  id: number
  code: string
  name: string
  kind: VehicleKind
  status: VehicleStatus
  lat: number | null
  lng: number | null
  speed_kmh: number
  last_seen_at: string | null
}

export interface VehicleList {
  vehicles: Vehicle[]
  count: number
}

/** Frames pushed over /api/vehicles/stream (see internal/handler/vehicles.go). */
export type VehicleFrame =
  | { type: 'snapshot'; vehicles: Vehicle[]; at: string }
  | { type: 'position'; vehicle: Vehicle; at: string }
  | { type: 'route'; route: VehicleRoute; at: string }
  | { type: 'route_cleared'; route_id: number; vehicle_id: number; at: string }
  | { type: 'route_completed'; route_id: number; vehicle_id: number; at: string }

/** A unit without a reported position must not get a marker. */
export function hasPosition(v: Vehicle): v is Vehicle & { lat: number; lng: number } {
  return typeof v.lat === 'number' && typeof v.lng === 'number' && Number.isFinite(v.lat) && Number.isFinite(v.lng)
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
