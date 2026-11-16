import { useState, type FormEvent, type ReactNode } from 'react';
import { ApiError } from '../api/client';
import { connect } from '../state/session';
import { Logo } from './Logo';

/** The sign-in form: on its own page, or in the landing page's dialog. */
export function LoginForm({ note, onCancel }: { note?: ReactNode; onCancel?: () => void }) {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!token.trim()) return;
    setBusy(true);
    setError('');
    try {
      await connect(token);
    } catch (err) {
      const status = err instanceof ApiError ? err.status : 0;
      setError(status === 401 ? 'That token was not accepted. Check it and try again.' : 'Cannot reach the Gjallarhorn control API. Is it running?');
    } finally {
      setBusy(false);
    }
  };

  return (
      <form className="login-card" onSubmit={submit} aria-labelledby="login-title">
        <Logo size={44} />
        <h1 id="login-title">Gjallarhorn console</h1>
        {note ?? <p className="muted">Watch every agent call crossing your Bifrost gateway, stop attacks and lock agents.</p>}
        <label htmlFor="token">Admin token</label>
        <input
          id="token"
          type="password"
          autoComplete="off"
          spellCheck={false}
          value={token}
          onChange={(e) => setToken(e.target.value)}
          aria-invalid={!!error}
          aria-describedby={error ? 'login-error' : 'login-hint'}
          autoFocus
        />
        <p id="login-hint" className="hint">
          The control API's admin token. It stays in this tab only and is sent in a header, never in a URL.
        </p>
        {error && (
          <p id="login-error" className="error" role="alert">
            {error}
          </p>
        )}
        <button className="btn primary" type="submit" disabled={busy || !token.trim()}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
        {onCancel && (
          <button className="btn ghost" type="button" onClick={onCancel}>
            Back
          </button>
        )}
      </form>
  );
}

export function Login() {
  return (
    <div className="login">
      <LoginForm />
    </div>
  );
}
