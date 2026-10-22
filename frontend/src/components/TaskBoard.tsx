import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Entity, Task, TaskInput, TaskStatus, UserAccount } from '../lib/types'
import {
  formatTaskDue,
  isTaskOverdue,
  normalizeTaskStatus,
  validateTaskInput,
} from '../lib/types'
import Select from './Select'
import DatePicker from './DatePicker'
import {
  IconAlert,
  IconCheck,
  IconClock,
  IconEdit,
  IconMapPin,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconTrash,
  IconUsers,
  IconX,
} from './Icons'

/** The three columns of the board, in the order the work walks through them. */
const COLUMNS: { status: TaskStatus; label: string; hint: string }[] = [
  { status: 'pending', label: 'Pending', hint: 'Not started yet' },
  { status: 'in_progress', label: 'In Progress', hint: 'On the road' },
  { status: 'completed', label: 'Done', hint: 'Finished' },
]

/** The work order the form is editing. */
interface Draft {
  id: number | null
  title: string
  description: string
  status: TaskStatus
  assignedUserId: number | null
  placeId: number | null
  dueDate: string
}

interface Props {
  tasks: Task[]
  loading: boolean
  error: string | null
  /** The places of the workspace: the destinations a work order points at. */
  places: Entity[]
  /** The accounts a work order can be handed to: drivers and staff. */
  assignees: UserAccount[]
  /** Admin: may create, edit and remove work orders. */
  canManage: boolean
  /** Driver and staff: may move a work order along. */
  canMove: boolean
  onRetry: () => void
  onCreate: (input: TaskInput) => Promise<void>
  onUpdate: (id: number, input: TaskInput) => Promise<void>
  onDelete: (task: Task) => void
  onStatus: (id: number, status: TaskStatus) => Promise<void>
}

function emptyDraft(): Draft {
  return {
    id: null,
    title: '',
    description: '',
    status: 'pending',
    assignedUserId: null,
    placeId: null,
    dueDate: '',
  }
}

function draftFrom(task: Task): Draft {
  return {
    id: task.id,
    title: task.title,
    description: task.description,
    status: normalizeTaskStatus(task.status),
    assignedUserId: task.assigned_user_id,
    placeId: task.place_id,
    dueDate: task.due_date ?? '',
  }
}

/** The name an account is shown under. */
export function accountName(user: UserAccount): string {
  return user.name?.trim() || user.username || user.email || `Account #${user.id}`
}

/**
 * The task board: the work orders of the field, in three columns.
 *
 * A card is a job: what has to be done, who does it, where it goes and by
 * when. The admin fills the board, the driver and the staff move the cards
 * along — that is the whole work of the day.
 */
export default function TaskBoard({
  tasks,
  loading,
  error,
  places,
  assignees,
  canManage,
  canMove,
  onRetry,
  onCreate,
  onUpdate,
  onDelete,
  onStatus,
}: Props) {
  const [query, setQuery] = useState('')
  const [draft, setDraft] = useState<Draft | null>(null)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [busyId, setBusyId] = useState<number | null>(null)
  /** Phone: one column at a time, picked with the tabs above the board. */
  const [activeCol, setActiveCol] = useState<TaskStatus>('pending')

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return tasks
    return tasks.filter((t) =>
      [t.title, t.description, t.assigned_name, t.place_name].some((v) =>
        (v ?? '').toLowerCase().includes(needle),
      ),
    )
  }, [tasks, query])

  const columns = useMemo(() => {
    const grouped = new Map<TaskStatus, Task[]>()
    for (const col of COLUMNS) grouped.set(col.status, [])
    for (const t of filtered) {
      const list = grouped.get(normalizeTaskStatus(t.status))
      if (list) list.push(t)
    }
    return grouped
  }, [filtered])

  function openCreate() {
    setDraft(emptyDraft())
    setFields({})
    setFormError(null)
  }

  function openEdit(task: Task) {
    setDraft(draftFrom(task))
    setFields({})
    setFormError(null)
  }

  function closeForm() {
    if (saving) return
    setDraft(null)
    setFields({})
    setFormError(null)
  }

  useEffect(() => {
    if (!draft) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !e.defaultPrevented) closeForm()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft, saving])

  async function submit() {
    if (!draft) return
    const input: TaskInput = {
      title: draft.title,
      description: draft.description,
      status: draft.status,
      assigned_user_id: draft.assignedUserId,
      place_id: draft.placeId,
      due_date: draft.dueDate,
    }
    const found = validateTaskInput(input)
    setFields(found)
    if (Object.keys(found).length > 0) return

    setSaving(true)
    setFormError(null)
    try {
      if (draft.id === null) {
        await onCreate(input)
      } else {
        await onUpdate(draft.id, input)
      }
      setDraft(null)
      setFields({})
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Could not save the task.')
    } finally {
      setSaving(false)
    }
  }

  /** Move a card to the next step (or back to pending). */
  async function move(task: Task, status: TaskStatus) {
    setBusyId(task.id)
    try {
      await onStatus(task.id, status)
    } catch {
      // The error is reported by the caller; the card stays where it was.
    } finally {
      setBusyId(null)
    }
  }

  const assigneeOptions = [
    { value: '', label: 'Unassigned' },
    ...assignees.map((u) => ({
      value: String(u.id),
      label: `${accountName(u)} · ${u.role === 'driver' ? 'Driver' : 'Staff'}`,
    })),
  ]

  const placeOptions = [
    { value: '', label: 'No place' },
    ...places.map((p) => ({ value: String(p.id), label: p.name })),
  ]

  return (
    <section className="tb" aria-label="Tasks">
      <div className="tb-toolbar">
        <label className="search tb-search">
          <IconSearch width={17} height={17} />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search task, place or assignee"
            aria-label="Search tasks"
          />
        </label>
        <div className="tb-actions">
          <button
            type="button"
            className="icon-btn"
            onClick={onRetry}
            disabled={loading}
            title="Reload tasks"
            aria-label="Reload tasks"
          >
            <IconRefresh width={16} height={16} />
          </button>
          {canManage && (
            <button type="button" className="btn primary tb-add" onClick={openCreate}>
              <IconPlus width={16} height={16} />
              New task
            </button>
          )}
        </div>
      </div>

      {error && (
        <div className="tb-empty" role="alert">
          <IconAlert width={18} height={18} />
          <span>{error}</span>
          <button type="button" className="btn secondary small" onClick={onRetry}>
            Try again
          </button>
        </div>
      )}

      {!error && loading && tasks.length === 0 && (
        <div className="tb-board" aria-busy="true">
          {COLUMNS.map((col) => (
            <div key={col.status} className="tb-col">
              <div className="tb-skeleton" />
              <div className="tb-skeleton" />
            </div>
          ))}
        </div>
      )}

      {!error && !loading && tasks.length === 0 && (
        <div className="tb-empty">
          <IconCheck width={20} height={20} />
          <span>No tasks yet. {canManage ? 'Create the first work order.' : 'Nothing to do right now.'}</span>
        </div>
      )}

      {!error && tasks.length > 0 && filtered.length === 0 && (
        <div className="tb-empty">
          <IconSearch width={18} height={18} />
          <span>No task matches this search.</span>
        </div>
      )}

      {filtered.length > 0 && (
        <div className="tb-tabs" role="tablist" aria-label="Task status">
          {COLUMNS.map((col) => (
            <button
              key={col.status}
              type="button"
              role="tab"
              aria-selected={activeCol === col.status}
              className={`tb-tab ${col.status}${activeCol === col.status ? ' active' : ''}`}
              onClick={() => setActiveCol(col.status)}
            >
              <i aria-hidden="true" />
              {col.label}
              <b>{(columns.get(col.status) ?? []).length}</b>
            </button>
          ))}
        </div>
      )}

      {filtered.length > 0 && (
        <div className="tb-board">
          {COLUMNS.map((col) => {
            const list = columns.get(col.status) ?? []
            return (
              <section
                key={col.status}
                className={`tb-col ${col.status}${activeCol === col.status ? ' current' : ''}`}
                aria-label={col.label}
              >
                <header className="tb-col-head">
                  <span className="tb-col-title">
                    <i aria-hidden="true" />
                    {col.label}
                  </span>
                  <span className="tb-col-count">{list.length}</span>
                </header>
                <p className="tb-col-hint">{col.hint}</p>
                {list.length === 0 ? (
                  <p className="tb-col-empty">Nothing here.</p>
                ) : (
                  <ul className="tb-cards">
                    {list.map((task) => (
                      <TaskCard
                        key={task.id}
                        task={task}
                        canManage={canManage}
                        canMove={canMove}
                        busy={busyId === task.id}
                        onEdit={openEdit}
                        onDelete={onDelete}
                        onMove={move}
                      />
                    ))}
                  </ul>
                )}
              </section>
            )
          })}
        </div>
      )}

      {draft &&
        createPortal(
          <div className="tb-overlay" onClick={closeForm}>
            <div
              className="tb-modal"
              role="dialog"
              aria-modal="true"
              aria-labelledby="task-form-title"
              onClick={(e) => e.stopPropagation()}
            >
              <header className="tb-modal-head">
                <div className="tb-modal-title">
                  <h3 id="task-form-title">{draft.id === null ? 'New task' : 'Edit task'}</h3>
                  <p>
                    {draft.id === null
                      ? 'Describe the job, hand it to someone and pick the destination.'
                      : 'Change the job, the assignee or the deadline.'}
                  </p>
                </div>
                <button type="button" className="tb-modal-close" onClick={closeForm} aria-label="Close">
                  <IconX width={18} height={18} />
                </button>
              </header>

              <div className="tb-modal-body">
                <div className="tb-field">
                  <label htmlFor="task-title">Title</label>
                  <input
                    id="task-title"
                    value={draft.title}
                    maxLength={160}
                    autoComplete="off"
                    autoFocus
                    onChange={(e) => setDraft({ ...draft, title: e.target.value })}
                    placeholder="Antar 3 palet oli ke Gudang Utama"
                    aria-invalid={!!fields.title}
                  />
                  {fields.title ? (
                    <span className="tb-error">{fields.title}</span>
                  ) : (
                    <span className="tb-hint">One line: what has to be done.</span>
                  )}
                </div>

                <div className="tb-field">
                  <label htmlFor="task-desc">Description</label>
                  <textarea
                    id="task-desc"
                    value={draft.description}
                    rows={3}
                    maxLength={2000}
                    onChange={(e) => setDraft({ ...draft, description: e.target.value })}
                    placeholder="What to bring, who to call, what to hand over."
                  />
                  {fields.description && <span className="tb-error">{fields.description}</span>}
                </div>

                <div className="tb-grid">
                  <div className="tb-field">
                    <span className="tb-label" id="task-assignee-label">
                      Assignee
                    </span>
                    <Select
                      id="task-assignee"
                      label="Assignee"
                      value={draft.assignedUserId == null ? '' : String(draft.assignedUserId)}
                      options={assigneeOptions}
                      onChange={(v) => setDraft({ ...draft, assignedUserId: v ? Number(v) : null })}
                    />
                    <span className="tb-hint">The driver or the staff member who does the job.</span>
                  </div>

                  <div className="tb-field">
                    <span className="tb-label" id="task-place-label">
                      Place
                    </span>
                    <Select
                      id="task-place"
                      label="Place"
                      value={draft.placeId == null ? '' : String(draft.placeId)}
                      options={placeOptions}
                      onChange={(v) => setDraft({ ...draft, placeId: v ? Number(v) : null })}
                    />
                    <span className="tb-hint">Where the work has to happen.</span>
                  </div>
                </div>

                <div className="tb-grid">
                  <div className="tb-field">
                    <span className="tb-label" id="task-status-label">
                      Status
                    </span>
                    <Select
                      id="task-status"
                      label="Status"
                      value={draft.status}
                      options={COLUMNS.map((c) => ({ value: c.status, label: c.label }))}
                      onChange={(v) => setDraft({ ...draft, status: normalizeTaskStatus(v) })}
                    />
                  </div>

                  <div className="tb-field">
                    <span className="tb-label" id="task-due-label">
                      Deadline
                    </span>
                    <div className="tb-due">
                      <DatePicker
                        value={draft.dueDate}
                        onChange={(v) => setDraft({ ...draft, dueDate: v })}
                        ariaLabel="Deadline"
                      />
                      {draft.dueDate && (
                        <button
                          type="button"
                          className="tb-link"
                          onClick={() => setDraft({ ...draft, dueDate: '' })}
                        >
                          Clear
                        </button>
                      )}
                    </div>
                    {fields.due_date ? (
                      <span className="tb-error">{fields.due_date}</span>
                    ) : (
                      <span className="tb-hint">The day the job has to be done by.</span>
                    )}
                  </div>
                </div>

                {formError && (
                  <div className="tb-alert" role="alert">
                    <IconAlert width={16} height={16} />
                    <span>{formError}</span>
                  </div>
                )}
              </div>

              <footer className="tb-modal-foot">
                <button type="button" className="btn secondary" onClick={closeForm} disabled={saving}>
                  Cancel
                </button>
                <button type="button" className="btn primary" onClick={() => void submit()} disabled={saving}>
                  {saving ? 'Saving…' : draft.id === null ? 'Create task' : 'Save changes'}
                </button>
              </footer>
            </div>
          </div>,
          document.body,
        )}
    </section>
  )
}

/**
 * The work orders of a driver, as a plain list.
 *
 * The driver screen is made for a phone in one hand: three columns of a
 * board do not fit there, so the driver gets the same jobs as a list — and
 * the button that closes a job.
 */
export function TaskList({
  tasks,
  loading,
  error,
  busyId,
  onStatus,
  onRetry,
}: {
  tasks: Task[]
  loading: boolean
  error: string | null
  busyId: number | null
  onStatus: (id: number, status: TaskStatus) => Promise<void>
  onRetry: () => void
}) {
  const open = tasks.filter((t) => normalizeTaskStatus(t.status) !== 'completed')
  const done = tasks.filter((t) => normalizeTaskStatus(t.status) === 'completed')

  if (error) {
    return (
      <div className="tb-empty" role="alert">
        <IconAlert width={16} height={16} />
        <span>{error}</span>
        <button type="button" className="btn secondary small" onClick={onRetry}>
          Try again
        </button>
      </div>
    )
  }

  if (loading && tasks.length === 0) {
    return <p className="tb-empty">Loading your tasks…</p>
  }

  if (tasks.length === 0) {
    return <p className="tb-empty">No tasks for you right now.</p>
  }

  return (
    <ul className="tb-list">
      {[...open, ...done].map((task) => {
        const status = normalizeTaskStatus(task.status)
        const overdue = status !== 'completed' && isTaskOverdue(task.due_date)
        return (
          <li key={task.id} className={`tb-list-item ${status}`}>
            <div className="tb-list-main">
              <b title={task.title}>{task.title}</b>
              <span className="tb-list-meta">
                {task.place_name && (
                  <span>
                    <IconMapPin width={11} height={11} />
                    {task.place_name}
                  </span>
                )}
                <span className={overdue ? 'over' : undefined}>
                  <IconClock width={11} height={11} />
                  {formatTaskDue(task.due_date)}
                </span>
              </span>
            </div>
            {status !== 'completed' ? (
              <button
                type="button"
                className="btn primary small tb-done"
                onClick={() => void onStatus(task.id, 'completed')}
                disabled={busyId === task.id}
              >
                {busyId === task.id ? 'Saving…' : 'Mark done'}
              </button>
            ) : (
              <span className="tb-done-flag">
                <IconCheck width={14} height={14} />
                Done
              </span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/** One work order on the board. */
function TaskCard({
  task,
  canManage,
  canMove,
  busy,
  onEdit,
  onDelete,
  onMove,
}: {
  task: Task
  canManage: boolean
  canMove: boolean
  busy: boolean
  onEdit: (task: Task) => void
  onDelete: (task: Task) => void
  onMove: (task: Task, status: TaskStatus) => Promise<void>
}) {
  const status = normalizeTaskStatus(task.status)
  const overdue = status !== 'completed' && isTaskOverdue(task.due_date)

  return (
    <li className={`tb-card${busy ? ' busy' : ''}`}>
      <div className="tb-card-head">
        <h4 title={task.title}>{task.title}</h4>
        {canManage && (
          <span className="tb-card-tools">
            <button
              type="button"
              className="icon-btn"
              onClick={() => onEdit(task)}
              aria-label={`Edit ${task.title}`}
              title="Edit task"
            >
              <IconEdit width={15} height={15} />
            </button>
            <button
              type="button"
              className="icon-btn"
              onClick={() => onDelete(task)}
              aria-label={`Delete ${task.title}`}
              title="Delete task"
            >
              <IconTrash width={15} height={15} />
            </button>
          </span>
        )}
      </div>

      {task.description && <p className="tb-card-desc">{task.description}</p>}

      <div className="tb-card-meta">
        {task.place_name && (
          <span className="tb-chip place">
            <IconMapPin width={13} height={13} />
            <span>{task.place_name}</span>
          </span>
        )}
        <span className={`tb-chip due${overdue ? ' over' : ''}`}>
          <IconClock width={13} height={13} />
          <span>{formatTaskDue(task.due_date)}</span>
        </span>
      </div>

      {(task.assigned_name || canMove) && (
        <div className="tb-card-foot">
          {task.assigned_name ? (
            <span className="tb-who" title={task.assigned_name}>
              <span className="tb-avatar" aria-hidden="true">
                {task.assigned_name.trim().charAt(0).toUpperCase()}
              </span>
              <span className="tb-who-name">{task.assigned_name}</span>
            </span>
          ) : (
            <span />
          )}

          {canMove && (
            <div className="tb-card-actions">
              {status === 'pending' && (
                <button
                  type="button"
                  className="btn secondary small"
                  onClick={() => void onMove(task, 'in_progress')}
                  disabled={busy}
                >
                  Start
                </button>
              )}
              {status !== 'completed' && (
                <button
                  type="button"
                  className="btn primary small"
                  onClick={() => void onMove(task, 'completed')}
                  disabled={busy}
                >
                  Mark done
                </button>
              )}
              {status === 'completed' && (
                <button
                  type="button"
                  className="btn secondary small"
                  onClick={() => void onMove(task, 'pending')}
                  disabled={busy}
                >
                  Reopen
                </button>
              )}
            </div>
          )}
        </div>
      )}
    </li>
  )
}
