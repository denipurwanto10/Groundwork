import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import type { UserAccount, UserInput } from '../lib/types'
import { ApiError } from '../lib/api'
import Select from './Select'
import { IconAlert, IconEdit, IconPlus, IconRefresh, IconSearch, IconTrash, IconUsers, IconX } from './Icons'

/** User types an admin can hand out. Mirrors the CHECK on users.role. */
const ROLE_OPTIONS = [
  { value: 'admin', label: 'Admin', hint: 'Full access: vehicles, zones and accounts.' },
  { value: 'staff', label: 'Staff', hint: 'Works the task board: picks up and closes work orders.' },
  { value: 'viewer', label: 'Viewer', hint: 'Read-only access to the live map.' },
  { value: 'driver', label: 'Driver', hint: 'Only sees the vehicles assigned to them.' },
] as const

type RoleValue = (typeof ROLE_OPTIONS)[number]['value']

const GENDER_OPTIONS = [
  { value: '', label: 'Not set' },
  { value: 'male', label: 'Male' },
  { value: 'female', label: 'Female' },
] as const

const FILTERS: { key: 'all' | RoleValue; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'admin', label: 'Admin' },
  { key: 'staff', label: 'Staff' },
  { key: 'viewer', label: 'Viewer' },
  { key: 'driver', label: 'Driver' },
]

const ROLE_RANK: Record<string, number> = { admin: 0, staff: 1, viewer: 2, driver: 3 }
const USERNAME_PATTERN = /^[a-zA-Z0-9._-]+$/

interface Draft {
  id: number | null
  username: string
  name: string
  email: string
  gender: string
  role: RoleValue
  password: string
}

interface Props {
  users: UserAccount[]
  loading: boolean
  error: string | null
  /** Id of the signed-in admin, so the panel can protect their own account. */
  currentUserId: number | null
  onRetry: () => void
  onCreate: (input: UserInput) => Promise<void>
  onUpdate: (id: number, input: Partial<UserInput>) => Promise<void>
  onDelete: (user: UserAccount) => void
}

function emptyDraft(): Draft {
  return { id: null, username: '', name: '', email: '', gender: '', role: 'driver', password: '' }
}

function draftFrom(user: UserAccount): Draft {
  return {
    id: user.id,
    username: user.username ?? '',
    name: user.name ?? '',
    email: user.email ?? '',
    gender: user.gender ?? '',
    role: (user.role as RoleValue) ?? 'viewer',
    password: '',
  }
}

function displayName(user: UserAccount): string {
  return user.name?.trim() || user.username || user.email || `Account #${user.id}`
}

function genderLabel(gender: string): string {
  return GENDER_OPTIONS.find((o) => o.value === gender)?.label ?? 'Not set'
}

function roleLabel(role: string): string {
  return ROLE_OPTIONS.find((o) => o.value === role)?.label ?? role
}

function validate(draft: Draft): Record<string, string> {
  const fields: Record<string, string> = {}
  const username = draft.username.trim()
  if (username.length < 3 || username.length > 64) {
    fields.username = 'Username must be 3-64 characters.'
  } else if (!USERNAME_PATTERN.test(username)) {
    fields.username = 'Use letters, digits, dot, dash or underscore only.'
  }
  if (draft.name.trim().length > 120) fields.name = 'Name must be 120 characters at most.'
  const email = draft.email.trim()
  if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) fields.email = 'Enter a valid email address.'
  if (draft.id === null && !draft.password) {
    fields.password = 'Set a password for the new account.'
  } else if (draft.password && draft.password.length < 8) {
    fields.password = 'Password must be at least 8 characters.'
  }
  return fields
}

/** Admin screen: register accounts and manage their user type and profile. */
export default function UserPanel({
  users,
  loading,
  error,
  currentUserId,
  onRetry,
  onCreate,
  onUpdate,
  onDelete,
}: Props) {
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<'all' | RoleValue>('all')
  const [draft, setDraft] = useState<Draft | null>(null)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [showPw, setShowPw] = useState(false)

  const counts = useMemo(() => {
    const base: Record<'all' | RoleValue, number> = { all: users.length, admin: 0, staff: 0, viewer: 0, driver: 0 }
    users.forEach((u) => {
      const role = u.role as RoleValue
      if (role in base) base[role] += 1
    })
    return base
  }, [users])

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return users
      .filter((u) => (filter === 'all' ? true : u.role === filter))
      .filter((u) => {
        if (!needle) return true
        return [u.username, u.name, u.email].some((v) => (v ?? '').toLowerCase().includes(needle))
      })
      .sort((a, b) => {
        const rank = (ROLE_RANK[a.role] ?? 9) - (ROLE_RANK[b.role] ?? 9)
        if (rank !== 0) return rank
        return displayName(a).localeCompare(displayName(b))
      })
  }, [users, filter, query])

  function openCreate() {
    setShowPw(false)
    setDraft(emptyDraft())
    setFields({})
    setFormError(null)
  }

  function openEdit(user: UserAccount) {
    setShowPw(false)
    setDraft(draftFrom(user))
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

  const isSelf = draft?.id !== null && draft?.id === currentUserId

  async function submit() {
    if (!draft) return
    const found = validate(draft)
    setFields(found)
    if (Object.keys(found).length > 0) return
    setSaving(true)
    setFormError(null)
    try {
      const payload: UserInput = {
        username: draft.username.trim(),
        name: draft.name.trim(),
        email: draft.email.trim(),
        gender: draft.gender,
        role: draft.role,
        password: draft.password,
      }
      if (draft.id === null) {
        await onCreate(payload)
      } else {
        await onUpdate(draft.id, payload)
      }
      setDraft(null)
      setFields({})
    } catch (err) {
      if (err instanceof ApiError) {
        setFormError(err.message)
        if (err.fields) setFields(err.fields)
      } else {
        setFormError(err instanceof Error ? err.message : 'Could not save the account.')
      }
    } finally {
      setSaving(false)
    }
  }


  return (
    <section className="um" aria-label="Users">
      <div className="um-stats" role="group" aria-label="Filter by user type">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            className={`um-stat ${f.key}${filter === f.key ? ' on' : ''}`}
            aria-pressed={filter === f.key}
            onClick={() => setFilter(f.key)}
          >
            <span className="um-stat-icon" aria-hidden="true">
              <IconUsers width={18} height={18} />
            </span>
            <span className="um-stat-text">
              <b>{counts[f.key]}</b>
              <span>{f.key === 'all' ? 'All accounts' : f.label}</span>
            </span>
          </button>
        ))}
      </div>

      <div className="um-toolbar">
        <label className="search um-search">
          <IconSearch width={17} height={17} />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search username, name or email"
            aria-label="Search accounts"
          />
        </label>
        <div className="um-actions">
          <button
            type="button"
            className="icon-btn"
            onClick={onRetry}
            disabled={loading}
            title="Reload accounts"
            aria-label="Reload accounts"
          >
            <IconRefresh width={16} height={16} />
          </button>
          <button type="button" className="btn primary um-add" onClick={openCreate}>
            <IconPlus width={16} height={16} />
            Add account
          </button>
        </div>
      </div>

      <div className="um-body">
        {error && (
          <div className="um-empty" role="alert">
            <IconAlert width={18} height={18} />
            <span>{error}</span>
            <button type="button" className="btn secondary small" onClick={onRetry}>
              Try again
            </button>
          </div>
        )}

        {!error && loading && users.length === 0 && (
          <ul className="um-grid" aria-busy="true" aria-label="Loading accounts">
            {[0, 1, 2, 3].map((i) => (
              <li key={i} className="um-skeleton" />
            ))}
          </ul>
        )}

        {!error && !loading && users.length === 0 && (
          <div className="um-empty">
            <IconUsers width={20} height={20} />
            <span>No accounts yet. Add the first one.</span>
          </div>
        )}

        {!error && users.length > 0 && filtered.length === 0 && (
          <div className="um-empty">
            <IconSearch width={18} height={18} />
            <span>No account matches this filter.</span>
          </div>
        )}

        {filtered.length > 0 && (
          <ul className="um-grid">
            {filtered.map((u) => {
              const role = (ROLE_OPTIONS.find((o) => o.value === u.role)?.value ?? 'viewer') as RoleValue
              const isMe = u.id === currentUserId
              return (
                <li key={u.id} className={`um-card${isMe ? ' me' : ''}`}>
                  <div className="um-card-top">
                    <span className={`um-avatar ${role}`} aria-hidden="true">
                      {displayName(u).charAt(0).toUpperCase()}
                    </span>
                    <span className="um-id">
                      <b title={displayName(u)}>
                        {displayName(u)}
                        {isMe && <em> (you)</em>}
                      </b>
                      <span>@{u.username || '—'}</span>
                    </span>
                    <span className={`role-badge ${role}`}>{roleLabel(u.role)}</span>
                  </div>

                  <dl className="um-meta">
                    <div>
                      <dt>Email</dt>
                      <dd title={u.email || undefined}>{u.email || '—'}</dd>
                    </div>
                    <div>
                      <dt>Gender</dt>
                      <dd>{u.gender ? genderLabel(u.gender) : '—'}</dd>
                    </div>
                  </dl>

                  <div className="um-card-foot">
                    <button
                      type="button"
                      className="btn secondary small"
                      onClick={() => openEdit(u)}
                      aria-label={`Edit ${displayName(u)}`}
                    >
                      <IconEdit width={14} height={14} />
                      Edit
                    </button>
                    <button
                      type="button"
                      className="btn secondary small um-del"
                      onClick={() => onDelete(u)}
                      disabled={isMe}
                      aria-label={`Delete ${displayName(u)}`}
                      title={isMe ? 'You cannot delete your own account' : 'Delete account'}
                    >
                      <IconTrash width={14} height={14} />
                      Delete
                    </button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      {draft &&
        createPortal(
          <div className="um-overlay" onClick={closeForm}>
            <div
              className="um-modal"
              role="dialog"
              aria-modal="true"
              aria-labelledby="user-form-title"
              onClick={(e) => e.stopPropagation()}
            >
              <span className="um-grabber" aria-hidden="true" />
              <header className="um-modal-head">
                <span className={`um-modal-avatar ${draft.role}`} aria-hidden="true">
                  {draft.id === null ? <IconPlus width={20} height={20} /> : (draft.name || draft.username || '?').charAt(0).toUpperCase()}
                </span>
                <div className="um-modal-title">
                  <h3 id="user-form-title">{draft.id === null ? 'Add account' : 'Edit account'}</h3>
                  <p>
                    {draft.id === null
                      ? 'Create a new sign-in and choose what it can access.'
                      : `Update the profile and access for @${draft.username || 'account'}.`}
                  </p>
                </div>
                <button type="button" className="um-modal-close" onClick={closeForm} aria-label="Close">
                  <IconX width={18} height={18} />
                </button>
              </header>

              <div className="um-modal-body">
                <div className="um-fgrid">
                  <div className="um-field">
                    <label htmlFor="user-username">Username</label>
                    <input
                      id="user-username"
                      value={draft.username}
                      maxLength={64}
                      autoComplete="off"
                      autoFocus
                      onChange={(e) => setDraft({ ...draft, username: e.target.value })}
                      placeholder="budi.santoso"
                      aria-invalid={!!fields.username}
                    />
                    {fields.username ? (
                      <span className="um-error">{fields.username}</span>
                    ) : (
                      <span className="um-hint">Used to sign in.</span>
                    )}
                  </div>

                  <div className="um-field">
                    <label htmlFor="user-name">Full name</label>
                    <input
                      id="user-name"
                      value={draft.name}
                      maxLength={120}
                      autoComplete="off"
                      onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                      placeholder="Budi Santoso"
                      aria-invalid={!!fields.name}
                    />
                    {fields.name && <span className="um-error">{fields.name}</span>}
                  </div>

                  <div className="um-field">
                    <label htmlFor="user-email">
                      Email <i>optional</i>
                    </label>
                    <input
                      id="user-email"
                      type="email"
                      value={draft.email}
                      maxLength={160}
                      autoComplete="off"
                      onChange={(e) => setDraft({ ...draft, email: e.target.value })}
                      placeholder="budi@example.com"
                      aria-invalid={!!fields.email}
                    />
                    {fields.email && <span className="um-error">{fields.email}</span>}
                  </div>

                  <div className="um-field">
                    <label htmlFor="user-gender">Gender</label>
                    <Select
                      id="user-gender"
                      label="Gender"
                      value={draft.gender}
                      options={GENDER_OPTIONS.map((o) => ({ value: o.value, label: o.label }))}
                      onChange={(v) => setDraft({ ...draft, gender: v })}
                    />
                  </div>
                </div>

                <div className="um-field">
                  <span className="um-label" id="user-role-label">
                    User type
                  </span>
                  <div className="um-roles" role="radiogroup" aria-labelledby="user-role-label">
                    {ROLE_OPTIONS.map((o) => (
                      <button
                        key={o.value}
                        type="button"
                        role="radio"
                        aria-checked={draft.role === o.value}
                        className={`um-role ${o.value}${draft.role === o.value ? ' on' : ''}`}
                        disabled={isSelf}
                        onClick={() => setDraft({ ...draft, role: o.value })}
                      >
                        <b>{o.label}</b>
                        <span>{o.hint}</span>
                      </button>
                    ))}
                  </div>
                  {fields.role ? (
                    <span className="um-error">{fields.role}</span>
                  ) : (
                    isSelf && <span className="um-hint">You cannot change your own user type.</span>
                  )}
                </div>

                <div className="um-field">
                  <label htmlFor="user-password">Password</label>
                  <div className="um-pw">
                    <input
                      id="user-password"
                      type={showPw ? 'text' : 'password'}
                      value={draft.password}
                      maxLength={72}
                      autoComplete="new-password"
                      onChange={(e) => setDraft({ ...draft, password: e.target.value })}
                      placeholder={draft.id === null ? 'At least 8 characters' : 'Leave blank to keep it'}
                      aria-invalid={!!fields.password}
                    />
                    <button type="button" onClick={() => setShowPw((v) => !v)} aria-pressed={showPw}>
                      {showPw ? 'Hide' : 'Show'}
                    </button>
                  </div>
                  {fields.password ? (
                    <span className="um-error">{fields.password}</span>
                  ) : (
                    <span className="um-hint">
                      {draft.id === null ? 'The account signs in with this password.' : 'Leave blank to keep the current password.'}
                    </span>
                  )}
                </div>

                {formError && (
                  <div className="um-alert" role="alert">
                    <IconAlert width={16} height={16} />
                    <span>{formError}</span>
                  </div>
                )}
              </div>

              <footer className="um-modal-foot">
                <button type="button" className="btn secondary" onClick={closeForm} disabled={saving}>
                  Cancel
                </button>
                <button type="button" className="btn primary" onClick={() => void submit()} disabled={saving}>
                  {saving ? 'Saving…' : draft.id === null ? 'Add account' : 'Save changes'}
                </button>
              </footer>
            </div>
          </div>,
          document.body,
        )}
    </section>
  )
}
