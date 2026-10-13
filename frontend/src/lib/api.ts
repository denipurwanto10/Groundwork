import type { Entity, EntityInput } from './types'

/** Base URL for the Go backend. Empty string = same origin (dev proxy). */
const BASE_URL = (import.meta.env.VITE_API_BASE_URL as string | undefined)?.replace(/\/$/, '') ?? ''

export type ApiErrorCode = 'validation_failed' | 'not_found' | 'invalid_id' | 'invalid_json' | 'http_error' | 'network_error'

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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${BASE_URL}${path}`, {
      headers: { 'Content-Type': 'application/json' },
      ...init,
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
    throw new ApiError(code, body.error?.message ?? `Request failed with status ${res.status}.`, res.status, body.error?.fields)
  }

  return body.data as T
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

export const api = {
  health(): Promise<{ status: string; checks: { database: string }; timestamp: string }> {
    return request('/health')
  },
  listEntities(): Promise<Entity[]> {
    return request<Entity[]>('/api/entities')
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
}
