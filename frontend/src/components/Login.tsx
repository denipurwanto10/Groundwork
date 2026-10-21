import { useRef, useState, type FormEvent } from 'react'
import { useAuth } from '../lib/auth'

/** Sign-in form. Renders standalone; AuthGate supplies the page shell. */
export default function Login() {
  const { login } = useAuth()
  const [identifier, setIdentifier] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState({ identifier: false, password: false })
  const identifierRef = useRef<HTMLInputElement>(null)
  const passwordRef = useRef<HTMLInputElement>(null)

  const identifierError = !identifier.trim() ? 'Enter your username or email.' : null
  const passwordError = !password ? 'Enter your password.' : null
  const touch = (k: 'identifier' | 'password') => setTouched((t) => ({ ...t, [k]: true }))

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (identifierError || passwordError) {
      setTouched({ identifier: true, password: true })
      setError(null)
      ;(identifierError ? identifierRef : passwordRef).current?.focus()
      return
    }
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
          ref={identifierRef}
          type="text"
          name="identifier"
          autoComplete="username"
          value={identifier}
          onChange={(e) => setIdentifier(e.target.value)}
          onBlur={() => touch('identifier')}
          placeholder="Enter your username"
          aria-invalid={touched.identifier && !!identifierError}
          aria-describedby={touched.identifier && identifierError ? 'identifier-error' : undefined}
          autoFocus
        />
        {touched.identifier && identifierError && (
          <span className="field-error" id="identifier-error" role="alert">
            {identifierError}
          </span>
        )}
      </label>

      <label className="field">
        <span className="label-text">Password</span>
        <input
          ref={passwordRef}
          type="password"
          name="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onBlur={() => touch('password')}
          placeholder="Enter your password"
          aria-invalid={touched.password && !!passwordError}
          aria-describedby={touched.password && passwordError ? 'password-error' : undefined}
        />
        {touched.password && passwordError && (
          <span className="field-error" id="password-error" role="alert">
            {passwordError}
          </span>
        )}
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