import { Api, ApiError } from '../api/client';
import { followStream } from '../api/sse';
import { store } from './store';

const TOKEN_KEY = 'gj.token';

let api: Api | null = null;
let stop: (() => void) | null = null;

/** The API client for the signed-in session. */
export function getApi(): Api {
  if (!api) throw new Error('not signed in');
  return api;
}

export function savedToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

/**
 * Signs in with the admin token: checks it, loads principals, lockdowns, mode and recent
 * history, then follows the live stream. The token is kept in sessionStorage, so it is gone
 * when the tab closes.
 */
export async function connect(token: string): Promise<void> {
  disconnect();
  const a = new Api(token.trim());
  store.set({ conn: 'connecting' });
  try {
    await a.lockdowns(); // the cheapest authenticated call: checks the token
  } catch (e) {
    // Back to the sign-in page, which explains what went wrong.
    store.set({ conn: e instanceof ApiError && e.status === 401 ? 'unauthorized' : 'idle' });
    throw e;
  }
  api = a;
  try {
    sessionStorage.setItem(TOKEN_KEY, token.trim());
  } catch {
    // Not remembered; the session still works.
  }

  const [principals, lockdowns, enforcing, stats, copilot] = await Promise.all([
    a.principals(),
    a.lockdowns(),
    a.mode(),
    a.stats(),
    a.copilot().catch(() => null),
  ]);
  store.setPrincipals(principals);
  store.set({ lockdowns, enforcing, copilot });
  const backlog = await a.events(Math.max(0, stats.last_seq - 5000), 5000);
  store.addEvents(backlog, false);

  a.scenarios()
    .then(() => store.set({ demo: true }))
    .catch(() => store.set({ demo: false }));

  stop = followStream({
    token: token.trim(),
    after: () => store.lastSeq(),
    onEvent: (e) => store.addEvents([e], true),
    onStatus: (conn) => {
      store.set({ conn });
      if (conn === 'live') {
        // Catch up on anything that changed while we were away.
        void a.lockdowns().then((lockdowns) => store.set({ lockdowns }));
        void a.mode().then((enforcing) => store.set({ enforcing }));
        void a.principals().then((p) => store.setPrincipals(p));
      }
    },
  });
}

export function disconnect(): void {
  stop?.();
  stop = null;
  api = null;
}

export function signOut(): void {
  disconnect();
  try {
    sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    // Nothing stored.
  }
  store.reset();
}

/** Runs an API action, turning failures into a toast. Returns undefined on failure. */
export async function act<T>(what: string, fn: (a: Api) => Promise<T>): Promise<T | undefined> {
  try {
    return await fn(getApi());
  } catch (e) {
    const err = e as ApiError;
    store.toast('bad', `${what} failed`, err.details?.length ? `${err.message}: ${err.details.join('; ')}` : err.message);
    return undefined;
  }
}
