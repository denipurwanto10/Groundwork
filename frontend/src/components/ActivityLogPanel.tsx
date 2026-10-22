import { useEffect, useMemo, useState } from 'react'
import type { AuditLog } from '../lib/types'
import Select from './Select'
import { IconAlert, IconHistory, IconRefresh, IconSearch } from './Icons'

/** The actions the trail knows, with the word the panel shows for them. */
const ACTIONS: { value: string; label: string }[] = [
  { value: '', label: 'All actions' },
  { value: 'create', label: 'Created' },
  { value: 'update', label: 'Updated' },
  { value: 'delete', label: 'Deleted' },
  { value: 'import', label: 'Imported' },
  { value: 'set', label: 'Route planned' },
  { value: 'clear', label: 'Route cleared' },
]

/** How many rows one page of the trail holds. */
export const AUDIT_PAGE_SIZE = 50

interface Props {
  logs: AuditLog[]
  /** How many entries the trail holds behind the current filter. */
  total: number
  loading: boolean
  error: string | null
  /** The action the panel is narrowed to ('' = every action). */
  action: string
  /** The text the panel searches for. */
  query: string
  /** How many rows are skipped (the current page starts here). */
  offset: number
  onActionChange: (action: string) => void
  onQueryChange: (query: string) => void
  onOffsetChange: (offset: number) => void
  onRetry: () => void
}

/** "2026-10-22T05:54:29+07:00" -> "22 Oct 2026, 05:54:29" in the local time. */
function formatMoment(iso: string): string {
  const parsed = new Date(iso)
  if (Number.isNaN(parsed.getTime())) return iso
  return parsed.toLocaleString(undefined, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

/** The short name of the account behind an entry. */
function actorOf(log: AuditLog): string {
  const identity = log.user_email?.trim()
  if (identity) return identity
  return log.user_id ? `account #${log.user_id}` : 'unknown'
}

/**
 * The trail of successful writes: who changed what, and when.
 *
 * Admin-only — the panel is behind the admin tab, and the API refuses the
 * list to everybody else.
 */
export default function ActivityLogPanel({
  logs,
  total,
  loading,
  error,
  action,
  query,
  offset,
  onActionChange,
  onQueryChange,
  onOffsetChange,
  onRetry,
}: Props) {
  // The text in the box and the text that is searched for are two things:
  // the box follows the keyboard, the search waits for it to settle.
  const [text, setText] = useState(query)

  useEffect(() => {
    if (text === query) return
    const timer = window.setTimeout(() => onQueryChange(text), 300)
    return () => window.clearTimeout(timer)
  }, [text, query, onQueryChange])

  const shown = useMemo(() => logs.length, [logs])

  return (
    <section className="al" aria-label="Activity log">
      <div className="al-toolbar">
        <label className="search al-search">
          <IconSearch width={17} height={17} />
          <input
            type="search"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Search a user or a message"
            aria-label="Search the activity log"
          />
        </label>
        <div className="al-actions">
          <Select
            id="al-action"
            label="Action"
            value={action}
            options={ACTIONS}
            onChange={(v) => onActionChange(v)}
            className="al-filter"
          />
          <button
            type="button"
            className="icon-btn"
            onClick={onRetry}
            disabled={loading}
            title="Reload the activity log"
            aria-label="Reload the activity log"
          >
            <IconRefresh width={16} height={16} />
          </button>
        </div>
      </div>

      <div className="al-body">
        {error && (
          <div className="al-empty" role="alert">
            <IconAlert width={18} height={18} />
            <span>{error}</span>
            <button type="button" className="btn secondary small" onClick={onRetry}>
              Try again
            </button>
          </div>
        )}

        {!error && loading && logs.length === 0 && (
          <ul className="al-skeleton" aria-busy="true" aria-label="Loading the activity log">
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <li key={i} />
            ))}
          </ul>
        )}

        {!error && !loading && logs.length === 0 && (
          <div className="al-empty">
            <IconHistory width={20} height={20} />
            <span>Nothing logged yet. Changes appear here as soon as somebody makes one.</span>
          </div>
        )}

        {logs.length > 0 && (
          <ul className="al-list">
            {logs.map((log) => (
              <li key={log.id} className="al-entry">
                <div className="al-entry-head">
                  <span className={`al-badge ${log.action}`}>{log.action}</span>
                  <span className="al-details">{log.details}</span>
                </div>
                <div className="al-entry-meta">
                  <span className="al-actor" title={actorOf(log)}>
                    {actorOf(log)}
                  </span>
                  <span className="al-dot" aria-hidden="true">
                    ·
                  </span>
                  <time className="al-when" dateTime={log.created_at}>
                    {formatMoment(log.created_at)}
                  </time>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>

      {total > AUDIT_PAGE_SIZE && (
        <footer className="al-foot">
          <span className="al-count">
            {offset + 1}–{Math.min(offset + shown, total)} of {total}
          </span>
          <div className="al-pager">
            <button
              type="button"
              className="btn secondary small"
              disabled={offset === 0}
              onClick={() => onOffsetChange(Math.max(0, offset - AUDIT_PAGE_SIZE))}
            >
              Newer
            </button>
            <button
              type="button"
              className="btn secondary small"
              disabled={offset + AUDIT_PAGE_SIZE >= total}
              onClick={() => onOffsetChange(offset + AUDIT_PAGE_SIZE)}
            >
              Older
            </button>
          </div>
        </footer>
      )}
    </section>
  )
}
