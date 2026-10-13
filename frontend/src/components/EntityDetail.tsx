import type { Entity } from '../lib/types'
import { IconClose, IconEdit, IconPin, IconTrash } from './Icons'

interface Props {
  entity: Entity | undefined
  deleting: boolean
  onEdit: () => void
  onDelete: () => void
  onClose: () => void
}

function formatDate(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

export default function EntityDetail({ entity, deleting, onEdit, onDelete, onClose }: Props) {
  // The detail card floats over the map, so nothing is rendered until a selection exists.
  if (!entity) return null

  return (
    <section className="glass detail-panel" aria-label={`Details for ${entity.name}`}>
      <span className="sheet-grip" aria-hidden="true" />

      <div className="detail-head">
        <span className={`entity-avatar large ${entity.status}`} aria-hidden="true">
          <IconPin width={24} height={24} />
        </span>
        <div className="detail-title">
          <h2>{entity.name}</h2>
          <p className="detail-sub">
            <span className="detail-type">{entity.type}</span>
          </p>
        </div>
        <button className="icon-btn" onClick={onClose} aria-label="Close details">
          <IconClose />
        </button>
      </div>

      <dl className="detail-grid">
        <div className="cell">
          <dt>Status</dt>
          <dd>
            <span className={`badge ${entity.status}`}>{entity.status}</span>
          </dd>
        </div>
        <div className="cell">
          <dt>Type</dt>
          <dd>{entity.type}</dd>
        </div>
        <div className="cell">
          <dt>Latitude</dt>
          <dd className="num">{entity.latitude}</dd>
        </div>
        <div className="cell">
          <dt>Longitude</dt>
          <dd className="num">{entity.longitude}</dd>
        </div>
        <div className="cell">
          <dt>Created</dt>
          <dd>{formatDate(entity.created_at)}</dd>
        </div>
        <div className="cell">
          <dt>Updated</dt>
          <dd>{formatDate(entity.updated_at)}</dd>
        </div>
      </dl>

      <div className="detail-actions">
        <button className="btn danger" onClick={onDelete} disabled={deleting}>
          <IconTrash width={18} height={18} />
          <span>{deleting ? 'Deleting…' : 'Delete'}</span>
        </button>
        <button className="btn primary" onClick={onEdit}>
          <IconEdit width={18} height={18} />
          <span>Edit</span>
        </button>
      </div>
    </section>
  )
}
