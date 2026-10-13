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
