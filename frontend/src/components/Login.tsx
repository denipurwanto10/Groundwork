import { useState, type FormEvent } from 'react'
import { useAuth } from '../lib/auth'

/** Sign-in form. Renders standalone; AuthGate supplies the page shell. */
export default function Login() {
  const { login } = useAuth()
  const [identifier, setIdentifier] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(identifier.trim(), password)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not sign in.')
      setBusy(false)
    }
  }

  return (
    <form className="auth-form" onSubmit={onSubmit} noValidate>
      <h2 className="auth-title">Sign in</h2>
      <p className="auth-lede">Sign in to open the map.</p>

      <label className="field">
        <span className="label-text">Username or email</span>
        <input
          type="text"
          name="identifier"
          autoComplete="username"
          value={identifier}
          onChange={(e) => setIdentifier(e.target.value)}
          placeholder="budi.santoso or you@example.com"
          autoFocus
          required
        />
      </label>

      <label className="field">
        <span className="label-text">Password</span>
        <input
          type="password"
          name="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
      </label>

      {error && (
        <p className="auth-error" role="alert">
          {error}
        </p>
      )}

      <button className="btn primary auth-submit" type="submit" disabled={busy}>
        {busy ? 'Signing in…' : 'Sign in'}
      </button>

      <p className="auth-switch">No account yet? Ask an admin to create one for you.</p>
    </form>
  )
}