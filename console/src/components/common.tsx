import type { Decision, Plane } from '../api/types';
import { planeName, shortName } from '../model/flights';
import { confirmAction } from './Dialog';
import { act } from '../state/session';
import { store } from '../state/store';

export function DecisionBadge({ d, monitored }: { d: Decision; monitored?: boolean }) {
  if (d.effect === 'allow') {
    return (
      <span className="badge allowed">
        <span aria-hidden="true">✓</span> Allowed
      </span>
    );
  }
  const key = d.rule_id || d.code;
  return monitored ? (
    <span className="badge flagged" title={d.reason}>
      <span aria-hidden="true">⚠</span> Flagged · {key}
    </span>
  ) : (
    <span className="badge blocked" title={d.reason}>
      <span aria-hidden="true">✕</span> Blocked · {key}
    </span>
  );
}

export function PlaneTag({ plane }: { plane: Plane }) {
  return <span className={`plane ${plane}`}>{planeName(plane)}</span>;
}

/** origin › hop › actor → target, as pills. */
export function Route({ chain, target, stopped }: { chain: string[]; target: string; stopped?: boolean }) {
  return (
    <ol className="route" aria-label="Chain">
      {chain.map((id, i) => (
        <li key={`${id}-${i}`} className="pill">
          {shortName(id)}
        </li>
      ))}
      <li className={`pill target${stopped ? ' stopped' : ''}`}>
        <span aria-hidden="true">{stopped ? '✕ ' : '→ '}</span>
        {target}
      </li>
    </ol>
  );
}

/** Asks for a reason (and how long), then locks the principal. */
export async function lockPrincipal(id: string): Promise<boolean> {
  const res = await confirmAction({
    title: `Lock ${id}?`,
    body: (
      <p>
        Every call {id} makes, and every chain it appears in, will be refused on its next call: models, tools and other agents. People who use it keep their own access.
      </p>
    ),
    confirmLabel: 'Lock now',
    danger: true,
    input: { label: 'Reason (recorded in the flight recorder)', initial: 'Locked from the console while investigating' },
    choice: {
      label: 'For how long',
      initial: '',
      options: [
        { value: '', label: 'Until someone lifts it' },
        { value: '15m', label: '15 minutes' },
        { value: '1h', label: '1 hour' },
        { value: '24h', label: '24 hours' },
      ],
    },
  });
  if (!res) return false;
  const done = await act(`Locking ${id}`, (a) =>
    a.request('POST', '/v1/lockdowns', { principal: id, reason: res.text, ...(res.choice ? { duration: res.choice } : {}) }),
  );
  if (done) {
    store.toast('warn', `${shortName(id)} is locked`, res.choice ? `For ${res.choice}.` : 'Until someone lifts it.');
    store.set({ lockdowns: await act('Refreshing lockdowns', (a) => a.lockdowns()) ?? store.get().lockdowns });
  }
  return !!done;
}

export async function unlockPrincipal(id: string): Promise<void> {
  const done = await act(`Unlocking ${id}`, async (a) => {
    await a.unlock(id);
    return true;
  });
  if (done) {
    store.toast('good', `${shortName(id)} is unlocked`);
    store.set((s) => ({ lockdowns: s.lockdowns.filter((l) => l.principal !== id) }));
  }
}
