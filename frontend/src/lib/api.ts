import type {
  Entity,
  EntityInput,
  ZoneInput,
  ZoneFeature,
  ZoneCollection,
  ZoneCount,
  ZoneImportReport,
  RegionFeature,
  RegionCollection,
  RegionCount,
  Vehicle,
  VehicleInput,
  VehicleList,
  VehicleRoute,
  RouteList,
  RouteStop,
  IslandList,
  UserAccount,
  UserInput,
  GeofenceEvent,
  AuditLogList,
  LoginResult,
  AuthStatus,
  HistoryPoint,
  Overview,
  Report,
  ReportParams,
  Task,
  TaskInput,
  TaskStatus,
} from './types'
import { clearRefreshToken, clearToken, getRefreshToken, getToken, notifySessionInvalid, setRefreshToken, setToken } from './session'

/** Base URL for the Go backend. Empty string = same origin (dev proxy). */
const BASE_URL = (import.meta.env.VITE_API_BASE_URL as string | undefined)?.replace(/\/$/, '') ?? ''

export type ApiErrorCode =
  | 'validation_failed'
  | 'not_found'
  | 'invalid_id'
  | 'invalid_json'
  | 'http_error'
  | 'network_error'
  | 'unauthorized'
  | 'forbidden'
  | 'username_taken'
  | 'email_taken'
  | 'last_admin'
  | 'self_delete'
  | 'self_role_change'
  | 'invalid_credentials'
  | 'too_many_attempts'
  | 'setup_closed'

export class ApiError extends Error {
  code: ApiErrorCode
  status: number
  fields?: Record<string, string>

  constructor(code: ApiErrorCode, message: string, status = 0, fields?: Record<string, string>) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
    this.fields = fields
  }
}

interface BackendErrorBody {
  error?: {
    code?: string
    message?: string
    fields?: Record<string, string>
  }
}

/**
 * The headers every authenticated call carries.
 *
 * The JWT lives in the session, so a call that is made without it is
 * refused by the server. A file download needs the same header: a plain link
 * would not carry the token, and the answer would be "unauthorized".
 */
function authHeaders(): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  return headers
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  // Every authenticated call carries the stored JWT.
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  let res: Response
  try {
    res = await fetch(`${BASE_URL}${path}`, {
      ...init,
      headers: { ...headers, ...(init?.headers as Record<string, string> | undefined) },
    })
  } catch {
    throw new ApiError('network_error', 'Cannot reach the backend. Is the Go server running?')
  }

  if (res.status === 204) {
    return undefined as T
  }

  let body: BackendErrorBody & { data?: T }
  try {
    body = (await res.json()) as BackendErrorBody & { data?: T }
  } catch {
    throw new ApiError('http_error', `Backend returned status ${res.status}.`, res.status)
  }

  if (!res.ok) {
    const code = (body.error?.code ?? 'http_error') as ApiErrorCode

    // An expired access token is not the end of the session: the refresh
    // token buys a new one and the request is tried again. Only when that
    // fails too does the session end everywhere.
    if (res.status === 401 && code === 'unauthorized' && !path.startsWith('/api/auth/')) {
      const renewed = await renewSession()
      if (renewed) {
        return request<T>(path, init)
      }
    }

    // A rejected token ends the session everywhere, not just on this request.
    if (res.status === 401 && (code === 'unauthorized' || code === 'invalid_credentials')) {
      clearToken()
      clearRefreshToken()
      notifySessionInvalid()
    }
    throw new ApiError(code, body.error?.message ?? `Request failed with status ${res.status}.`, res.status, body.error?.fields)
  }

  return body.data as T
}

/**
 * Renews the access token with the stored refresh token. Returns true when
 * the session lives on.
 *
 * Only one renewal runs at a time: a burst of parallel calls that all hit an
 * expired token would otherwise send a burst of refresh requests and only one
 * of them would be allowed to store its answer.
 */
let renewal: Promise<boolean> | null = null

function renewSession(): Promise<boolean> {
  if (renewal) return renewal
  renewal = doRenew().finally(() => {
    renewal = null
  })
  return renewal
}

async function doRenew(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false

  try {
    const res = await fetch(`${BASE_URL}/api/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
    if (!res.ok) {
      clearRefreshToken()
      return false
    }
    const body = (await res.json()) as { data?: { token?: string; refresh_token?: string } }
    const token = body.data?.token
    if (!token) {
      clearRefreshToken()
      return false
    }
    setToken(token)
    if (body.data?.refresh_token) setRefreshToken(body.data.refresh_token)
    return true
  } catch {
    // The backend is unreachable: the request fails on its own, the session
    // stays as it is.
    return false
  }
}

function toPayload(input: EntityInput) {
  return {
    name: input.name.trim(),
    type: input.type.trim(),
    status: input.status === '' ? 'active' : input.status,
    latitude: input.latitude,
    longitude: input.longitude,
  }
}

function toVehiclePayload(input: VehicleInput) {
  return {
    code: input.code.trim(),
    name: input.name.trim(),
    kind: input.kind,
    driver_id: input.driver_id,
    route_color: input.route_color,
  }
}

/**
 * Account payload. Only the fields present in `input` are sent, so a partial
 * update leaves the rest of the account alone; an empty password means "keep
 * the current one".
 */
function toUserPayload(input: Partial<UserInput>) {
  const body: Record<string, string> = {}
  if (input.username !== undefined) body.username = input.username.trim()
  if (input.email !== undefined) body.email = input.email.trim()
  if (input.password) body.password = input.password
  if (input.name !== undefined) body.name = input.name.trim()
  if (input.gender !== undefined) body.gender = input.gender
  if (input.role !== undefined) body.role = input.role
  return body
}

/**
 * Task payload. The empty strings become null: a work order without an
 * assignee and without a place is a work order that nobody owns yet.
 */
function toTaskPayload(input: TaskInput) {
  return {
    title: input.title.trim(),
    description: input.description.trim(),
    status: input.status,
    assigned_user_id: input.assigned_user_id ?? null,
    place_id: input.place_id ?? null,
    due_date: input.due_date || '',
  }
}

/**
 * The name of a downloaded file, as the server asks for it.
 *
 * The header is only readable when the answer comes from the same origin, so
 * a name of our own is the fallback.
 */
function filenameFromDisposition(header: string | null): string {
  const match = header?.match(/filename="?([^";]+)"?/i)
  return match ? match[1] : 'groundwork-report.csv'
}

export const api = {
  health(): Promise<{ status: string; checks: { database: string }; timestamp: string }> {
    return request('/health')
  },
  listEntities(zoneId?: number | null): Promise<Entity[]> {
    return request<Entity[]>(zoneId ? `/api/entities?zone=${zoneId}` : '/api/entities')
  },
  getEntity(id: number): Promise<Entity> {
    return request<Entity>(`/api/entities/${id}`)
  },
  createEntity(input: EntityInput): Promise<Entity> {
    return request<Entity>('/api/entities', {
      method: 'POST',
      body: JSON.stringify(toPayload(input)),
    })
  },
  updateEntity(id: number, input: EntityInput): Promise<Entity> {
    return request<Entity>(`/api/entities/${id}`, {
      method: 'PUT',
      body: JSON.stringify(toPayload(input)),
    })
  },
  deleteEntity(id: number): Promise<void> {
    return request<void>(`/api/entities/${id}`, { method: 'DELETE' })
  },

  // ---- Zones (operator-drawn polygons) ----

  listZones(): Promise<ZoneCollection> {
    return request<ZoneCollection>('/api/zones')
  },

  getZone(id: number): Promise<ZoneFeature> {
    return request<ZoneFeature>(`/api/zones/${id}`)
  },

  /**
   * The live counters of every zone: how many places and how many vehicles
   * are inside. Far lighter than the zone list, so the map can keep the
   * numbers up to date without reloading the geometry.
   */
  listZoneCounts(): Promise<ZoneCount[]> {
    return request<{ zones: ZoneCount[] }>('/api/zones/counts').then((data) => data.zones)
  },

  createZone(input: ZoneInput): Promise<ZoneFeature> {
    return request<ZoneFeature>('/api/zones', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },

  /**
   * Create a zone from an official boundary instead of a hand-drawn one.
   * admin_regions stays read-only: the polygon is copied into zones.
   */
  zoneFromRegion(input: {
    region_code: string
    name?: string
    category?: string
    color?: string
    description?: string
  }): Promise<ZoneFeature> {
    return request<ZoneFeature>('/api/zones/from-region', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },

  /** Island groups (union of their province boundaries). */
  listIslands(): Promise<IslandList> {
    return request<IslandList>('/api/islands')
  },

  /** Create one zone covering every province of an island group. */
  zoneFromIsland(input: {
    island: string
    name?: string
    category?: string
    color?: string
    description?: string
  }): Promise<ZoneFeature> {
    return request<ZoneFeature>('/api/zones/from-island', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },

  updateZone(id: number, input: ZoneInput): Promise<ZoneFeature> {
    return request<ZoneFeature>(`/api/zones/${id}`, {
      method: 'PUT',
      body: JSON.stringify(input),
    })
  },

  deleteZone(id: number): Promise<void> {
    return request<void>(`/api/zones/${id}`, { method: 'DELETE' })
  },

  importZones(collection: ZoneCollection): Promise<ZoneImportReport> {
    return request<ZoneImportReport>('/api/zones/import', {
      method: 'POST',
      body: JSON.stringify(collection),
    })
  },

  // ---- Official administrative regions (read-only) ----

  listRegions(params?: {
    level?: number
    parent?: string
    geom?: boolean
    precision?: number
    /** Name search; the server caps the result (default 40). */
    q?: string
    limit?: number
  }): Promise<RegionCollection> {
    const sp = new URLSearchParams()
    if (params?.level != null) sp.set('level', String(params.level))
    if (params?.parent) sp.set('parent', params.parent)
    if (params?.geom != null) sp.set('geom', params.geom ? 'true' : 'false')
    if (params?.precision != null) sp.set('precision', String(params.precision))
    if (params?.q) sp.set('q', params.q)
    if (params?.limit != null) sp.set('limit', String(params.limit))
    const qs = sp.toString()
    return request<RegionCollection>(`/api/regions${qs ? `?${qs}` : ''}`)
  },

  getRegion(code: string, level: number, geom = true, precision = 5): Promise<RegionFeature> {
    const sp = new URLSearchParams()
    if (!geom) sp.set('geom', 'false')
    if (precision != null) sp.set('precision', String(precision))
    const qs = sp.toString()
    return request<RegionFeature>(`/api/regions/${code}?level=${level}${qs ? `&${qs}` : ''}`)
  },

  regionCount(params?: { level?: number; parent?: string }): Promise<RegionCount[]> {
    const sp = new URLSearchParams()
    if (params?.level != null) sp.set('level', String(params.level))
    if (params?.parent) sp.set('parent', params.parent)
    const qs = sp.toString()
    return request<RegionCount[]>(`/api/regions/count${qs ? `?${qs}` : ''}`)
  },

  // ---- Tracked vehicles (live tracking) ----

  /** Current fleet with the latest known position of each unit. */
  listVehicles(): Promise<VehicleList> {
    return request<VehicleList>('/api/vehicles')
  },

  /** Accounts an admin can hand a vehicle over to. */
  listUsers(): Promise<UserAccount[]> {
    return request<UserAccount[]>('/api/users')
  },

  /** Admin-only: register an account with its user type. */
  createUser(input: UserInput): Promise<UserAccount> {
    return request<UserAccount>('/api/users', {
      method: 'POST',
      body: JSON.stringify(toUserPayload(input)),
    })
  },

  /** Admin-only: change the given fields; an empty password keeps the old one. */
  updateUser(id: number, input: Partial<UserInput>): Promise<UserAccount> {
    return request<UserAccount>(`/api/users/${id}`, {
      method: 'PUT',
      body: JSON.stringify(toUserPayload(input)),
    })
  },

  /** Admin-only: remove an account. Its vehicles stay, without a driver. */
  deleteUser(id: number): Promise<void> {
    return request<void>(`/api/users/${id}`, { method: 'DELETE' })
  },

  createVehicle(input: VehicleInput): Promise<Vehicle> {
    return request<Vehicle>('/api/vehicles', {
      method: 'POST',
      body: JSON.stringify(toVehiclePayload(input)),
    })
  },

  updateVehicle(id: number, input: VehicleInput): Promise<Vehicle> {
    return request<Vehicle>(`/api/vehicles/${id}`, {
      method: 'PUT',
      body: JSON.stringify(toVehiclePayload(input)),
    })
  },

  deleteVehicle(id: number): Promise<void> {
    return request<void>(`/api/vehicles/${id}`, { method: 'DELETE' })
  },

  /** Report one position; the server appends it to the history and pushes it. */
  reportPosition(
    id: number,
    input: { lat: number; lng: number; speed_kmh?: number; status?: string; heading?: number | null },
  ): Promise<Vehicle> {
    return request<Vehicle>(`/api/vehicles/${id}/position`, {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },

  /**
   * The positions a unit reported in the past, oldest first.
   *
   * `date` picks one day (YYYY-MM-DD, in the time zone of the server);
   * `from`/`to` pick an explicit window. The server samples the points when a
   * day holds more of them than the browser can use.
   */
  vehicleHistory(
    id: number,
    params?: { date?: string; from?: string; to?: string; limit?: number },
  ): Promise<HistoryPoint[]> {
    const sp = new URLSearchParams()
    if (params?.date) sp.set('date', params.date)
    if (params?.from) sp.set('from', params.from)
    if (params?.to) sp.set('to', params.to)
    if (params?.limit != null) sp.set('limit', String(params.limit))
    const qs = sp.toString()
    return request<HistoryPoint[]>(`/api/vehicles/${id}/history${qs ? `?${qs}` : ''}`)
  },

  // ---- Geofence (zone boundary crossings) ----

  /** Crossing history, newest first; narrow it to one unit or one zone. */
  listGeofenceEvents(params?: {
    vehicle_id?: number
    zone_id?: number
    limit?: number
  }): Promise<GeofenceEvent[]> {
    const sp = new URLSearchParams()
    if (params?.vehicle_id != null) sp.set('vehicle_id', String(params.vehicle_id))
    if (params?.zone_id != null) sp.set('zone_id', String(params.zone_id))
    if (params?.limit != null) sp.set('limit', String(params.limit))
    const qs = sp.toString()
    return request<GeofenceEvent[]>(`/api/geofence-events${qs ? `?${qs}` : ''}`)
  },

  // ---- Work orders (the jobs the fleet is sent out for) ----

  /**
   * The task board. A driver only receives the tasks that are assigned to
   * them: the server narrows the list down to their own work.
   */
  listTasks(): Promise<Task[]> {
    return request<Task[]>('/api/tasks')
  },

  /** Admin-only: a new work order for the board. */
  createTask(input: TaskInput): Promise<Task> {
    return request<Task>('/api/tasks', {
      method: 'POST',
      body: JSON.stringify(toTaskPayload(input)),
    })
  },

  /** Admin-only: change every field of a work order. */
  updateTask(id: number, input: TaskInput): Promise<Task> {
    return request<Task>(`/api/tasks/${id}`, {
      method: 'PUT',
      body: JSON.stringify(toTaskPayload(input)),
    })
  },

  /** Admin-only: remove a work order. */
  deleteTask(id: number): Promise<void> {
    return request<void>(`/api/tasks/${id}`, { method: 'DELETE' })
  },

  /**
   * Move a work order along: pending -> in progress -> completed.
   *
   * A driver may do this on their own tasks, a staff member on any task.
   */
  updateTaskStatus(id: number, status: TaskStatus): Promise<Task> {
    return request<Task>(`/api/tasks/${id}/status`, {
      method: 'PUT',
      body: JSON.stringify({ status }),
    })
  },

  // ---- Audit log (who changed what) ----

  /**
   * The trail of successful writes, newest first. Admin-only: the trail
   * names the accounts of the workspace.
   */
  listAuditLogs(params?: {
    limit?: number
    offset?: number
    action?: string
    entity_type?: string
    /** Free text: a user or a fragment of the message. */
    q?: string
  }): Promise<AuditLogList> {
    const sp = new URLSearchParams()
    if (params?.limit != null) sp.set('limit', String(params.limit))
    if (params?.offset != null) sp.set('offset', String(params.offset))
    if (params?.action) sp.set('action', params.action)
    if (params?.entity_type) sp.set('entity_type', params.entity_type)
    if (params?.q) sp.set('q', params.q)
    const qs = sp.toString()
    return request<AuditLogList>(`/api/audit-logs${qs ? `?${qs}` : ''}`)
  },

  // ---- Overview (the aggregate of the day) ----

  /**
   * Everything the Overview tab shows: the distance the fleet drove today,
   * the fleet counted by status and the zone crossings of the day.
   */
  getOverview(): Promise<Overview> {
    return request<Overview>('/api/overview')
  },

  // ---- Reports (the operational report of a period) ----

  /**
   * The report of a period: the distance of every unit, the routes they
   * finished, the zone crossings and the work orders.
   *
   * The numbers are counted by the database; the browser only draws them.
   */
  getReport(params?: ReportParams): Promise<Report> {
    const sp = new URLSearchParams()
    if (params?.from) sp.set('from', params.from)
    if (params?.to) sp.set('to', params.to)
    if (params?.vehicle_id != null) sp.set('vehicle_id', String(params.vehicle_id))
    const qs = sp.toString()
    return request<Report>(`/api/reports${qs ? `?${qs}` : ''}`)
  },

  /**
   * The report as a CSV file.
   *
   * The file is fetched with the same authorization as every other call, so
   * the token travels with the request. A plain link would not carry it, and
   * the server would answer "unauthorized".
   */
  async downloadReportCSV(params?: ReportParams): Promise<{ blob: Blob; filename: string }> {
    const sp = new URLSearchParams()
    if (params?.from) sp.set('from', params.from)
    if (params?.to) sp.set('to', params.to)
    if (params?.vehicle_id != null) sp.set('vehicle_id', String(params.vehicle_id))
    const qs = sp.toString()
    const path = `/api/reports/export.csv${qs ? `?${qs}` : ''}`

    let res: Response
    try {
      res = await fetch(`${BASE_URL}${path}`, { headers: authHeaders() })
    } catch {
      throw new ApiError('network_error', 'Cannot reach the backend. Is the Go server running?')
    }

    // An expired access token is renewed and the file is asked for again.
    if (res.status === 401) {
      const renewed = await renewSession()
      if (renewed) {
        res = await fetch(`${BASE_URL}${path}`, { headers: authHeaders() })
      }
    }

    if (!res.ok) {
      let code: ApiErrorCode = 'http_error'
      let message = `The export failed with status ${res.status}.`
      try {
        const body = (await res.json()) as BackendErrorBody
        if (body.error?.code) code = body.error.code as ApiErrorCode
        if (body.error?.message) message = body.error.message
      } catch {
        // The answer was not the error format of the API; the status speaks.
      }
      throw new ApiError(code, message, res.status)
    }

    const blob = await res.blob()
    return { blob, filename: filenameFromDisposition(res.headers.get('Content-Disposition')) }
  },

  // ---- Delivery routes (migration 007) ----

  /** Every active route: the paths the map draws. */
  listRoutes(): Promise<RouteList> {
    return request<RouteList>('/api/routes')
  },

  /** Recent routes of one vehicle, newest first (active and finished). */
  listVehicleRoutes(id: number): Promise<RouteList> {
    return request<RouteList>(`/api/vehicles/${id}/routes`)
  },

  /**
   * Plan + store a route. Replaces any active route of the same vehicle.
   *
   * The destinations are sent as a list. The server works out the order they
   * are visited in and answers with the route, the stops in that order and
   * the line through all of them.
   */
  setVehicleRoute(id: number, input: { origin: RouteStop; destinations: RouteStop[] }): Promise<VehicleRoute> {
    return request<VehicleRoute>(`/api/vehicles/${id}/route`, {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },

  /**
   * Start the active route of a vehicle: the driver accepted the delivery and
   * the unit begins to drive it. Only the driver of the unit may do this.
   */
  startVehicleRoute(id: number): Promise<VehicleRoute> {
    return request<VehicleRoute>(`/api/vehicles/${id}/route/start`, { method: 'POST' })
  },

  clearVehicleRoute(id: number): Promise<void> {
    return request<void>(`/api/vehicles/${id}/route`, { method: 'DELETE' })
  },

  // ---- Authentication ----
  /** Sign in with either the username or the email address. */
  login(identifier: string, password: string): Promise<LoginResult> {
    return request<LoginResult>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: identifier.trim(), password }),
    })
  },

  /** Exchange the refresh token for a new access token. */
  refresh(refreshToken: string): Promise<LoginResult> {
    return request<LoginResult>('/api/auth/refresh', {
      method: 'POST',
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
  },

  /** Does the workspace still need its first account? */
  authStatus(): Promise<AuthStatus> {
    return request<AuthStatus>('/api/auth/status')
  },

  /** Create the first admin of a fresh workspace. */
  setup(input: { username: string; email?: string; password: string }): Promise<LoginResult> {
    return request<LoginResult>('/api/auth/setup', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },
}

/**
 * Absolute ws:// URL of the live feed. It has to go through whatever host and
 * port served the page (dev proxy included) because the backend restricts the
 * WebSocket origin to localhost — talking to :8090 directly from the page
 * would need CORS, which the Go server does not send.
 */
export function vehicleStreamUrl(): string {
  const url = new URL('/api/vehicles/stream', BASE_URL || window.location.origin)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}
