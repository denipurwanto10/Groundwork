import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './lib/api'
import type { Entity, EntityFieldErrors, EntityInput } from './lib/types'
import EntityMap from './components/EntityMap'
import EntityList from './components/EntityList'
import EntityDetail from './components/EntityDetail'
import EntityForm from './components/EntityForm'
import ConfirmDialog from './components/ConfirmDialog'
import { IconAlert, IconCheck, IconCrosshair, IconLayers, IconMoon, IconSun } from './components/Icons'
import './App.css'

type Toast = { kind: 'success' | 'error'; text: string } | null
type Theme = 'light' | 'dark'

const THEME_KEY = 'entity-manager:theme'

function getInitialTheme(): Theme {
  try {
    const saved = localStorage.getItem(THEME_KEY)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    /* storage unavailable */
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export default function App() {
  const [entities, setEntities] = useState<Entity[]>([])
  const [visibleEntities, setVisibleEntities] = useState<Entity[]>([])
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [backendDown, setBackendDown] = useState(false)
  const [toast, setToast] = useState<Toast>(null)
  const [theme, setTheme] = useState<Theme>(getInitialTheme)

  const [formOpen, setFormOpen] = useState(false)
  const [formMode, setFormMode] = useState<'create' | 'edit'>('create')
  const [saving, setSaving] = useState(false)
  const [serverFields, setServerFields] = useState<EntityFieldErrors>({})

  const [confirmOpen, setConfirmOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)

  const [picking, setPicking] = useState(false)
  const [draftCoords, setDraftCoords] = useState<{ latitude: number; longitude: number } | null>(null)

  const listSlotRef = useRef<HTMLDivElement>(null)

  const showToast = (kind: 'success' | 'error', text: string) => {
    setToast({ kind, text })
  }

  // Theme: applied before paint, persisted only when the person chooses one.
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme
    document.documentElement.style.colorScheme = theme
  }, [theme])

  const toggleTheme = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark'
    setTheme(next)
    try {
      localStorage.setItem(THEME_KEY, next)
    } catch {
      /* storage unavailable */
    }
  }

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const refresh = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      const data = await api.listEntities()
      setEntities(data)
      setBackendDown(false)
      setSelectedId((prev) => (prev != null && !data.some((e) => e.id === prev) ? null : prev))
    } catch (err) {
      // Gagal memuat daftar = backend bermasalah (mati, 5xx, dsb),
      // apa pun jenis error-nya — badge harus ikut offline.
      setBackendDown(true)
      setLoadError(err instanceof Error ? err.message : 'Could not load entities.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  const selected = entities.find((e) => e.id === selectedId)

  // The map shows filtered pins, but the open detail's pin must stay visible
  // even when the current filter would hide it.
  const mapEntities = useMemo(() => {
    if (selected && !visibleEntities.some((e) => e.id === selected.id)) {
      return [...visibleEntities, selected]
    }
    return visibleEntities
  }, [visibleEntities, selected])

  // While the form is docked over the list, the list underneath must not be reachable by keyboard.
  useEffect(() => {
    listSlotRef.current?.toggleAttribute('inert', formOpen)
  }, [formOpen])

  const openCreate = () => {
    setFormMode('create')
    setServerFields({})
    setDraftCoords(null)
    setPicking(true)
    setFormOpen(true)
  }

  const openEdit = () => {
    if (!selected) return
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

  const handleDelete = async () => {
    if (!selected) return
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

  // On phones the list sheet gives way to the detail sheet / form sheet.
  const covered = selected != null || formOpen

  return (
    <div className="app">
      <div className="map-stage">
        <EntityMap
          entities={mapEntities}
          selected={selected}
          onSelect={setSelectedId}
          picking={picking && formOpen}
          draftCoords={draftCoords}
          onPick={(lat, lng) => setDraftCoords({ latitude: lat, longitude: lng })}
          theme={theme}
        />
      </div>

      <aside className={covered ? 'sidebar is-covered' : 'sidebar'}>
        <header className="glass brandbar">
          <div className="brand">
            <span className="brand-mark">
              <IconLayers width={20} height={20} />
            </span>
            <div className="brand-text">
              <h1>Entity Manager</h1>
              <span className="brand-sub">Location workspace</span>
            </div>
          </div>
          <div className="brandbar-tools">
            <div
              className={backendDown ? 'backend-status down' : 'backend-status up'}
              role="status"
              title={backendDown ? 'The backend is not reachable' : 'Connected to the backend'}
            >
              <span className={backendDown ? 'dot down' : 'dot up'} aria-hidden="true" />
              <span className="status-label">{backendDown ? 'API offline' : 'API online'}</span>
            </div>
            <button
              className="icon-btn"
              onClick={toggleTheme}
              aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
              title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
            >
              {theme === 'dark' ? <IconSun /> : <IconMoon />}
            </button>
          </div>
        </header>

        <div className="list-slot" ref={listSlotRef}>
          <EntityList
            entities={entities}
            selectedId={selectedId}
            loading={loading}
            error={loadError}
            onSelect={setSelectedId}
            onAdd={openCreate}
            onRetry={refresh}
            onVisibleEntitiesChange={setVisibleEntities}
          />
        </div>
      </aside>

      {!formOpen && (
        <EntityDetail
          entity={selected}
          deleting={deleting}
          onEdit={openEdit}
          onDelete={() => setConfirmOpen(true)}
          onClose={() => setSelectedId(null)}
        />
      )}

      {picking && formOpen && (
        <div className="pick-hint" role="status">
          <IconCrosshair width={16} height={16} />
          <span>Pick a location</span>
          <button type="button" onClick={() => setPicking(false)}>
            Done
          </button>
        </div>
      )}

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

      <ConfirmDialog
        open={confirmOpen}
        title="Delete this entity?"
        message={selected ? `"${selected.name}" will be removed for good.` : ''}
        confirmLabel="Delete"
        busy={deleting}
        onConfirm={handleDelete}
        onCancel={() => setConfirmOpen(false)}
      />

      {toast && (
        <div className={`toast ${toast.kind}`} role={toast.kind === 'error' ? 'alert' : 'status'}>
          {toast.kind === 'error' ? <IconAlert width={18} height={18} /> : <IconCheck width={18} height={18} />}
          <span>{toast.text}</span>
        </div>
      )}
    </div>
  )
}
