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
