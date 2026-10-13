import { useEffect, useId, useState } from 'react'
import type { Entity, EntityFieldErrors, EntityInput } from '../lib/types'
import { emptyEntityInput, validateEntityInput } from '../lib/types'
import { IconClose, IconCrosshair } from './Icons'

interface Props {
  open: boolean
  mode: 'create' | 'edit'
  initial: Entity | null
  draftCoords: { latitude: number; longitude: number } | null
  saving: boolean
  picking: boolean
  serverFields: EntityFieldErrors
  onClose: () => void
  onSubmit: (input: EntityInput) => void
  onCoordsChange: () => void
  onTogglePick: () => void
}

function parseCoord(raw: string): number {
  return raw.trim() === '' ? NaN : Number(raw)
}

export default function EntityForm({
  open,
  mode,
  initial,
  draftCoords,
  saving,
  picking,
  serverFields,
  onClose,
  onSubmit,
  onCoordsChange,
  onTogglePick,
}: Props) {
  const uid = useId()
  const fid = (n: string) => `${uid}-${n}`

  const [name, setName] = useState('')
  const [type, setType] = useState('')
  const [status, setStatus] = useState<'active' | 'inactive'>('active')
  const [latRaw, setLatRaw] = useState('0')
  const [lngRaw, setLngRaw] = useState('0')
  const [touched, setTouched] = useState(false)

  // Reset when opened for a different entity/mode.
  useEffect(() => {
    if (open) {
      setName(initial?.name ?? emptyEntityInput.name)
      setType(initial?.type ?? emptyEntityInput.type)
      setStatus(initial?.status ?? 'active')
      setLatRaw(String(initial?.latitude ?? draftCoords?.latitude ?? 0))
      setLngRaw(String(initial?.longitude ?? draftCoords?.longitude ?? 0))
      setTouched(false)
      onCoordsChange()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, mode, initial?.id])

  // Map click fills the coordinate inputs.
  useEffect(() => {
    if (open && draftCoords) {
      setLatRaw(String(Number(draftCoords.latitude.toFixed(6))))
      setLngRaw(String(Number(draftCoords.longitude.toFixed(6))))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftCoords])

  if (!open) return null

  const input: EntityInput = {
    name,
    type,
    status,
    latitude: parseCoord(latRaw),
    longitude: parseCoord(lngRaw),
  }
  const clientErrors = touched ? validateEntityInput(input) : {}
  // Server errors (from backend validation) take precedence per field.
  const errors: EntityFieldErrors = { ...clientErrors, ...serverFields }
  const invalid = Object.keys(validateEntityInput(input)).length > 0

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setTouched(true)
    if (Object.keys(validateEntityInput(input)).length > 0) return
    onSubmit(input)
  }

  const a11y = (field: keyof EntityFieldErrors) => ({
    'aria-invalid': errors[field] ? true : undefined,
    'aria-describedby': errors[field] ? fid(`${field}-err`) : undefined,
  })

  const fieldError = (field: keyof EntityFieldErrors) =>
    errors[field] ? (
      <span id={fid(`${field}-err`)} className="field-error" role="alert">
        {errors[field]}
      </span>
    ) : null

  return (
    <div className="overlay dock">
      <form
        className="glass card form-card"
        onClick={(e) => e.stopPropagation()}
        onSubmit={handleSubmit}
        noValidate
        aria-labelledby={fid('title')}
      >
        <span className="sheet-grip" aria-hidden="true" />

        <div className="sheet-head">
          <div>
            <h2 id={fid('title')}>{mode === 'create' ? 'New entity' : 'Edit entity'}</h2>
            <p className="hint">Place the point on the map or enter coordinates.</p>
          </div>
          <button type="button" className="icon-btn" onClick={onClose} disabled={saving} aria-label="Close form">
            <IconClose />
          </button>
        </div>

        <div className="form-body">
          <div className="field">
            <label htmlFor={fid('name')} className="label-text">
              Name{' '}
              <span className="muted">
                {name.length}/255
              </span>
            </label>
            <input
              id={fid('name')}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Monas"
              maxLength={255}
              autoComplete="off"
              autoFocus={typeof window !== 'undefined' && window.matchMedia('(min-width: 768px)').matches}
              {...a11y('name')}
            />
            {fieldError('name')}
          </div>

          <div className="field">
            <label htmlFor={fid('type')} className="label-text">
              Type{' '}
              <span className="muted">
                {type.length}/100
              </span>
            </label>
            <input
              id={fid('type')}
              value={type}
              onChange={(e) => setType(e.target.value)}
              placeholder="Monument"
              maxLength={100}
              autoComplete="off"
              {...a11y('type')}
            />
            {fieldError('type')}
          </div>

          <fieldset className="field">
            <legend className="label-text">Status</legend>
            <div className="radio-group">
              {(['active', 'inactive'] as const).map((s) => (
                <label key={s} className="radio-opt">
                  <input
                    type="radio"
                    name={fid('status')}
                    value={s}
                    checked={status === s}
                    onChange={() => setStatus(s)}
                  />
                  <span className={`radio-box ${s}`}>
                    <span className="radio-dot" aria-hidden="true" />
                    {s}
                  </span>
                </label>
              ))}
            </div>
            {fieldError('status')}
          </fieldset>

          <fieldset className="field">
            <div className="group-head">
              <legend className="label-text">Location</legend>
              <button
                type="button"
                className={picking ? 'btn small primary' : 'btn small secondary'}
                aria-pressed={picking}
                onClick={onTogglePick}
              >
                <IconCrosshair width={16} height={16} />
                <span>{picking ? 'Picking…' : 'Pick on map'}</span>
              </button>
            </div>
            <div className="coord-row">
              <div className="field">
                <label htmlFor={fid('latitude')} className="sub-label">
                  Latitude
                </label>
                <input
                  id={fid('latitude')}
                  className="num"
                  value={latRaw}
                  onChange={(e) => setLatRaw(e.target.value)}
                  placeholder="-6.1754"
                  inputMode="decimal"
                  autoComplete="off"
                  {...a11y('latitude')}
                />
                {fieldError('latitude')}
              </div>
              <div className="field">
                <label htmlFor={fid('longitude')} className="sub-label">
                  Longitude
                </label>
                <input
                  id={fid('longitude')}
                  className="num"
                  value={lngRaw}
                  onChange={(e) => setLngRaw(e.target.value)}
                  placeholder="106.8272"
                  inputMode="decimal"
                  autoComplete="off"
                  {...a11y('longitude')}
                />
                {fieldError('longitude')}
              </div>
            </div>
          </fieldset>
        </div>

        <div className="form-actions form-footer">
          <button type="button" className="btn secondary" onClick={onClose} disabled={saving}>
            Cancel
          </button>
          <button type="submit" className="btn primary" disabled={saving || (touched && invalid)}>
            {saving ? 'Saving…' : mode === 'create' ? 'Create' : 'Save changes'}
          </button>
        </div>
      </form>
    </div>
  )
}
