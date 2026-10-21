import { useEffect, useId, useMemo, useRef, useState } from 'react'
import type {
  ZoneFeature,
  ZoneInput,
  ZoneGeometry,
  ZoneFieldErrors,
  ZoneImportReport,
  RegionFeature,
} from '../lib/types'
import {
  validateZoneInput,
  ZONE_CATEGORIES,
  ZONE_CATEGORY_LABELS,
  ZONE_CATEGORY_COLORS,
  DEFAULT_ZONE_COLOR,
  REGION_LEVELS,
} from '../lib/types'
import { MAX_EDITABLE_VERTICES, vertexTotal } from './ZoneLayer'
import Select from './Select'
import {
  IconPlus,
  IconDownload,
  IconUpload,
  IconTrash,
  IconEdit,
  IconX,
  IconPencil,
  IconPolygon,
  IconCheck,
  IconClose,
  IconAlert,
} from './Icons'

interface ZonePanelProps {
  zones: ZoneFeature[]
  selectedId: number | null
  loading: boolean
  error: string | null
  /** Geoman draw/edit tools are live on the map. */
  drawMode: boolean
  onDrawModeChange: (enabled: boolean) => void
  onSelect: (id: number | null) => void
  onCreate: (input: ZoneInput) => Promise<void>
  onUpdate: (id: number, input: ZoneInput) => Promise<void>
  onDelete: (id: number) => Promise<void>
  /** Import / export GeoJSON. */
  onImport: (file: File) => Promise<ZoneImportReport | null>
  onExport: () => Promise<void>
  /** Form visibility and the zone being edited are owned by App. */
  formOpen: boolean
  onFormOpenChange: (open: boolean) => void
  editingId: number | null
  onEditingIdChange: (id: number | null) => void
  /** Working geometry — drawn on the map or edited by dragging vertices. */
  geometry: ZoneGeometry | null
  onGeometryChange: (geometry: ZoneGeometry | null) => void
  /** Official regions available for the administrative link. */
  regions: RegionFeature[]
  /** Level of the region list currently loaded. */
  regionLevel: number
  /** Whether the current user may write zones (admin only). */
  canEdit: boolean
}

function formatArea(km2: number): string {
  if (!Number.isFinite(km2) || km2 <= 0) return '—'
  if (km2 < 1) return `${Math.round(km2 * 1000000).toLocaleString()} m²`
  return `${km2.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} km²`
}

export default function ZonePanel({
  zones,
  selectedId,
  loading,
  error,
  drawMode,
  onDrawModeChange,
  onSelect,
  onCreate,
  onUpdate,
  onDelete,
  onImport,
  onExport,
  formOpen,
  onFormOpenChange,
  editingId,
  onEditingIdChange,
  geometry,
  onGeometryChange,
  regions,
  regionLevel,
  canEdit,
}: ZonePanelProps) {
  const uid = useId()
  const fid = (name: string) => `${uid}-${name}`
  const fileInputRef = useRef<HTMLInputElement>(null)
  // Geometry as loaded from the server; used to tell an attribute edit from a
  // real reshape, so untouched shapes are never written back.
  const loadedShape = useRef<string>('')

  const [name, setName] = useState('')
  const [category, setCategory] = useState<ZoneInput['category']>('operational')
  const [color, setColor] = useState(DEFAULT_ZONE_COLOR)
  const [description, setDescription] = useState('')
  const [regionCode, setRegionCode] = useState<string | null>(null)
  const [regionLevelOfZone, setRegionLevelOfZone] = useState<number | null>(null)
  const [touched, setTouched] = useState(false)
  const [saving, setSaving] = useState(false)
  const [serverErrors, setServerErrors] = useState<ZoneFieldErrors>({})
  const [importing, setImporting] = useState(false)
  const [importReport, setImportReport] = useState<ZoneImportReport | null>(null)

  const isEditing = editingId !== null

  // Load the zone being edited into the form.
  useEffect(() => {
    if (editingId === null) return
    const zone = zones.find((z) => z.id === editingId)
    if (!zone) return
    const p = zone.properties
    setName(p.name)
    setCategory(p.category)
    setColor(p.color)
    setDescription(p.description)
    setRegionCode(p.region_code)
    setRegionLevelOfZone(p.region_level)
    onGeometryChange(zone.geometry)
    loadedShape.current = JSON.stringify(zone.geometry)
    setServerErrors({})
    setTouched(false)
  }, [editingId, zones, onGeometryChange])

  const input: ZoneInput = useMemo<ZoneInput>(
    () => ({
      name,
      category,
      color,
      description,
      region_level: regionLevelOfZone,
      region_code: regionCode,
      geometry,
    }),
    [name, category, color, description, regionLevelOfZone, regionCode, geometry],
  )

  const clientErrors = touched ? validateZoneInput(input) : {}
  const errors: ZoneFieldErrors = { ...clientErrors, ...serverErrors }
  const blockingErrors = validateZoneInput(input)
  const hasErrors = Object.keys(blockingErrors).length > 0

  const regionLevelLabel =
    REGION_LEVELS.find((l) => l.level === regionLevel)?.label.toLowerCase() ?? `level ${regionLevel}`

  const resetForm = () => {
    onEditingIdChange(null)
    setName('')
    setCategory('operational')
    setColor(DEFAULT_ZONE_COLOR)
    setDescription('')
    setRegionCode(null)
    setRegionLevelOfZone(null)
    setServerErrors({})
    setTouched(false)
    onGeometryChange(null)
    onFormOpenChange(false)
  }

  const startCreate = () => {
    onEditingIdChange(null)
    setName('')
    setCategory('operational')
    setColor(DEFAULT_ZONE_COLOR)
    setDescription('')
    setRegionCode(null)
    setRegionLevelOfZone(null)
    setServerErrors({})
    setTouched(false)
    onGeometryChange(null)
    onFormOpenChange(true)
    onDrawModeChange(true)
  }

  const startEdit = (id: number) => {
    onEditingIdChange(id)
    onSelect(id)
    onFormOpenChange(true)
    // Vertex editing on a 200k-point boundary hangs the map, so big zones open form-only.
    const zone = zones.find((item) => item.id === id)
    onDrawModeChange(vertexTotal(zone?.geometry) <= MAX_EDITABLE_VERTICES)
  }

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault()
    setTouched(true)
    const validation = validateZoneInput(input)
    if (Object.keys(validation).length > 0) return
    setSaving(true)
    setServerErrors({})
    try {
      if (editingId !== null) {
        // Only a changed outline is uploaded: a 200k-point boundary would
        // otherwise be rewritten (and re-simplified) on every rename.
        const attrs: ZoneInput = { name, category, color, description, region_level: regionLevelOfZone, region_code: regionCode }
        await onUpdate(editingId, JSON.stringify(geometry) === loadedShape.current ? attrs : input)
      } else {
        await onCreate(input)
      }
      resetForm()
      onDrawModeChange(false)
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Could not save the zone.'
      if (msg.includes('zones_geom_valid') || msg.toLowerCase().includes('invalid geometry')) {
        setServerErrors({ geometry: 'The polygon is invalid or self-intersecting.' })
      } else if (msg.includes('zones_color_check')) {
        setServerErrors({ color: 'Colour must be a 6-digit hex value.' })
      } else if (msg.includes('zones_category_check')) {
        setServerErrors({ category: 'Unknown category.' })
      } else if (msg.includes('zones_region_fk') || msg.toLowerCase().includes('region')) {
        setServerErrors({ region_code: 'That administrative region is not known to the server.' })
      } else {
        setServerErrors({ name: msg })
      }
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (id: number) => {
    if (!window.confirm('Delete this zone? This cannot be undone.')) return
    try {
      await onDelete(id)
      if (editingId === id) resetForm()
    } catch {
      // The failure is already reported by the shared toast in App.
    }
  }

  const handleImportFile = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setImporting(true)
    try {
      const report = await onImport(file)
      if (report) setImportReport(report)
    } finally {
      setImporting(false)
    }
  }

  const handleExport = async () => {
    try {
      await onExport()
    } catch (err) {
      window.alert(err instanceof Error ? err.message : 'Export failed.')
    }
  }

  const a11y = (field: keyof ZoneFieldErrors) => ({
    'aria-invalid': errors[field] ? true : undefined,
    'aria-describedby': errors[field] ? fid(`${field}-err`) : undefined,
  })

  const fieldError = (field: keyof ZoneFieldErrors) =>
    errors[field] ? (
      <span id={fid(`${field}-err`)} className="zone-form-error" role="alert">
        {errors[field]}
      </span>
    ) : null

  return (
    <div className="zone-panel">
      <div className="zone-panel-header">
        <h2>
          <IconPolygon width={20} height={20} />
          <span>Zones</span>
          <span className="count">{zones.length}</span>
        </h2>
        <div className="zone-actions">
          {canEdit && (
            <>
            <button
              type="button"
              className="icon-btn"
              onClick={() => onDrawModeChange(!drawMode)}
              aria-pressed={drawMode}
              title={drawMode ? 'Leave draw & edit mode' : 'Draw and edit polygons on the map'}
            >
              <IconPencil width={16} height={16} />
            </button>
            <button type="button" className="icon-btn" onClick={startCreate} title="New zone">
              <IconPlus width={16} height={16} />
            </button>
            <button
              type="button"
              className="icon-btn"
              onClick={() => fileInputRef.current?.click()}
              disabled={importing}
              title="Import zones from a GeoJSON file"
            >
              <IconUpload width={16} height={16} />
            </button>
            </>
          )}
          <button
            type="button"
            className="icon-btn"
            onClick={handleExport}
            title="Export all zones as GeoJSON"
          >
            <IconDownload width={16} height={16} />
          </button>
        </div>
      </div>

      <input
        type="file"
        ref={fileInputRef}
        accept=".geojson,.json,application/geo+json,application/json"
        hidden
        onChange={handleImportFile}
      />

      {drawMode && (
        <p className="zone-hint">
          Draw mode is on. On the map: draw a polygon, drag its vertices, drag the small midpoint
          markers to add a vertex, right-click a vertex to remove it, or use the bin to delete a
          polygon.
        </p>
      )}

      {importReport && (
        <div className="zone-import-report" role="status">
          <IconCheck width={16} height={16} />
          <span>
            Imported {importReport.imported} zone{importReport.imported === 1 ? '' : 's'}
            {importReport.failed.length > 0 ? `, ${importReport.failed.length} failed` : ''}
          </span>
          {importReport.failed.length > 0 && (
            <details>
              <summary>Details</summary>
              <ul>
                {importReport.failed.map((f) => (
                  <li key={f.index}>
                    {f.name || `#${f.index}`}: {f.reason}
                  </li>
                ))}
              </ul>
            </details>
          )}
          <button
            type="button"
            className="icon-btn"
            onClick={() => setImportReport(null)}
            aria-label="Dismiss import report"
          >
            <IconX width={14} height={14} />
          </button>
        </div>
      )}

      {error && (
        <div className="zone-import-report error" role="alert">
          <IconAlert width={16} height={16} />
          <span>{error}</span>
        </div>
      )}

      {loading ? (
        <p className="muted">Loading zones…</p>
      ) : zones.length === 0 ? (
        <div className="zone-empty">
          <span className="zone-empty-icon">
            <IconPolygon width={26} height={26} />
          </span>
          {canEdit ? (
            <>
              <p>No zones yet. Turn on the pencil and draw a polygon on the map.</p>
              <button type="button" className="btn primary" onClick={startCreate}>
                <IconPlus width={16} height={16} /> New zone
              </button>
            </>
          ) : (
            <p>No zones yet. Zones created by an admin will appear here.</p>
          )}
        </div>
      ) : (
        <ul className="zone-list">
          {zones.map((zone) => {
            const p = zone.properties
            const selected = zone.id === selectedId
            return (
              <li key={zone.id} className={selected ? 'selected' : undefined}>
                <button
                  type="button"
                  className={`zone-list-item${selected ? ' selected' : ''}`}
                  onClick={() => (canEdit ? startEdit(zone.id) : onSelect(zone.id))}
                >
                  <span className="zone-avatar" style={{ color: p.color }} aria-hidden="true">
                    <IconPolygon width={16} height={16} />
                  </span>
                  <span className="zone-body">
                    <span className="zone-name">{p.name}</span>
                    <span className="zone-meta">
                      <span className="zone-category">{ZONE_CATEGORY_LABELS[p.category]}</span>
                      <span>{formatArea(p.area_km2)}</span>
                      {p.region_name && (
                        <span className="zone-region">{p.region_name}</span>
                      )}
                      <span className="zone-counts">
                        {p.entity_count ?? 0} {(p.entity_count ?? 0) === 1 ? 'entity' : 'entities'} ·{' '}
                        {p.vehicle_count ?? 0} {(p.vehicle_count ?? 0) === 1 ? 'vehicle' : 'vehicles'}
                      </span>
                    </span>
                  </span>
                </button>
                {canEdit && (
                <span className="zone-actions">
                  <button
                    type="button"
                    className="icon-btn"
                    onClick={() => startEdit(zone.id)}
                    aria-label={`Edit ${p.name}`}
                    title="Edit"
                  >
                    <IconEdit width={14} height={14} />
                  </button>
                  <button
                    type="button"
                    className="icon-btn"
                    onClick={() => handleDelete(zone.id)}
                    aria-label={`Delete ${p.name}`}
                    title="Delete"
                  >
                    <IconTrash width={14} height={14} />
                  </button>
                </span>
                )}
              </li>
            )
          })}
        </ul>
      )}

      {formOpen && canEdit && (
        <div
          className="overlay dock"
          role="dialog"
          aria-labelledby={fid('title')}
        >
          <div className="card zone-form-card">
            <div className="zone-form-header">
              <h3 id={fid('title')}>{isEditing ? 'Edit zone' : 'New zone'}</h3>
              <button
                type="button"
                className="icon-btn"
                onClick={() => {
                  resetForm()
                  onDrawModeChange(false)
                }}
                aria-label="Close"
              >
                <IconClose width={18} height={18} />
              </button>
            </div>

            <form onSubmit={handleSubmit} noValidate>
              <div className="zone-form-body">
                <div className="zone-form-field">
                  <label className="zone-form-label" htmlFor={fid('name')}>
                    Name
                  </label>
                  <input
                    id={fid('name')}
                    className="zone-form-input"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Operational area alpha"
                    maxLength={120}
                    {...a11y('name')}
                  />
                  {fieldError('name')}
                </div>

                <div className="zone-form-field">
                  <label className="zone-form-label" htmlFor={fid('category')}>
                    Category
                  </label>
                  <Select
                    id={fid('category')}
                    label="Category"
                    value={category}
                    options={ZONE_CATEGORIES.map((c) => ({ value: c, label: ZONE_CATEGORY_LABELS[c] }))}
                    onChange={(v) => setCategory(v as ZoneInput['category'])}
                    invalid={!!errors.category}
                    describedBy={errors.category ? fid('category-err') : undefined}
                  />
                  {fieldError('category')}
                </div>

                <div className="zone-form-field">
                  <label className="zone-form-label" htmlFor={fid('color')}>
                    Colour
                  </label>
                  <input
                    id={fid('color')}
                    type="color"
                    className="zone-color-input"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                  />
                  <div className="zone-color-row">
                    {ZONE_CATEGORIES.map((c) => (
                      <button
                        key={c}
                        type="button"
                        className="zone-color-chip"
                        style={{ backgroundColor: ZONE_CATEGORY_COLORS[c] }}
                        title={ZONE_CATEGORY_LABELS[c]}
                        aria-label={`Use ${ZONE_CATEGORY_LABELS[c]} colour`}
                        onClick={() => setColor(ZONE_CATEGORY_COLORS[c])}
                      />
                    ))}
                  </div>
                  {fieldError('color')}
                </div>

                <div className="zone-form-field">
                  <label className="zone-form-label" htmlFor={fid('description')}>
                    Description
                  </label>
                  <textarea
                    id={fid('description')}
                    className="zone-form-textarea"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    placeholder="Optional notes"
                    maxLength={500}
                    rows={3}
                    {...a11y('description')}
                  />
                  {fieldError('description')}
                </div>

                <div className="zone-form-field">
                  <label className="zone-form-label" htmlFor={fid('region')}>
                    Administrative link
                  </label>
                  <div className="zone-region-select-row">
                    <Select
                      id={fid('region')}
                      label="Administrative link"
                      value={regionCode ?? ''}
                      options={[
                        { value: '', label: '— no administrative link —' },
                        ...regions.map((r) => ({ value: r.properties.code, label: r.properties.name })),
                      ]}
                      onChange={(v) => {
                        const code = v || null
                        setRegionCode(code)
                        const found = regions.find((r) => r.properties.code === code)
                        setRegionLevelOfZone(found ? found.properties.level : null)
                      }}
                      invalid={!!errors.region_code}
                      describedBy={errors.region_code ? fid('region_code-err') : undefined}
                    />
                  </div>
                  <p className="zone-hint">
                    Links the zone to an official boundary. Currently loaded: {regionLevelLabel}.
                  </p>
                  {fieldError('region_code')}
                </div>

                <div className="zone-form-field">
                  <span className="zone-form-label">Geometry</span>
                  {geometry ? (
                    <p className="zone-geometry-status ok">
                      <IconPolygon width={14} height={14} />
                      {geometry.type} · {vertexTotal(geometry).toLocaleString('en-US')} vertices
                    </p>
                  ) : (
                    <p className="zone-geometry-status empty">No geometry yet.</p>
                  )}
                  {vertexTotal(geometry) > MAX_EDITABLE_VERTICES ? (
                    <p className="zone-hint">
                      Too detailed to edit on the map. Name, category, colour and description still
                      save.
                    </p>
                  ) : (
                    <p className="zone-hint">
                      Draw the polygon on the map with the pencil tool, or edit its vertices while
                      draw mode is on.
                    </p>
                  )}
                  {fieldError('geometry')}
                </div>
              </div>

              <div className="zone-form-footer">
                <button
                  type="button"
                  className="btn secondary"
                  onClick={() => {
                    resetForm()
                    onDrawModeChange(false)
                  }}
                >
                  Cancel
                </button>
                <button type="submit" className="btn primary" disabled={saving || hasErrors}>
                  {saving ? 'Saving…' : isEditing ? 'Save changes' : 'Create zone'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
