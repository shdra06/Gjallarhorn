import { useMemo, useState } from 'react';
import { isNoise, kindOf } from '../model/flights';
import { useNow } from '../hooks';
import { useStore } from '../state/store';

/** Counts for the last minute: calls, blocked, flagged; plus locked agents. */
export function StatsHud() {
  const replay = useStore((s) => s.replay);
  const lockdowns = useStore((s) => s.lockdowns);
  const enforcing = useStore((s) => s.enforcing);
  const now = useNow(2000);
  const events = useStore((s) => s.events);
  const at = replay.active ? replay.cursor : now;

  const counts = useMemo(() => {
    const from = at - 60_000;
    let calls = 0;
    let blocked = 0;
    let flagged = 0;
    for (let i = events.length - 1; i >= 0; i--) {
      const e = events[i]!;
      const ts = Date.parse(e.ts);
      if (ts < from) break;
      if (ts > at || e.type !== 'decision' || isNoise(e)) continue;
      calls++;
      const k = kindOf(e);
      if (k === 'blocked') blocked++;
      if (k === 'flagged') flagged++;
    }
    return { calls, blocked, flagged };
  }, [at, events]);

  return (
    <section className="hud stats" aria-label="Last minute">
      <div className={`mode-badge ${enforcing === false ? 'monitor' : 'enforce'}`}>{enforcing === false ? 'MONITOR-ONLY' : 'ENFORCING'}</div>
      <dl>
        <div>
          <dt>Calls / min</dt>
          <dd>{counts.calls}</dd>
        </div>
        <div className="blocked">
          <dt>Blocked</dt>
          <dd>{counts.blocked}</dd>
        </div>
        <div className="flagged">
          <dt>Flagged</dt>
          <dd>{counts.flagged}</dd>
        </div>
        <div className="locked">
          <dt>Locked</dt>
          <dd>{lockdowns.length}</dd>
        </div>
      </dl>
    </section>
  );
}

export function Legend({ onReset }: { onReset: () => void }) {
  const [open, setOpen] = useState(true);
  return (
    <section className={`hud legend ${open ? '' : 'closed'}`} aria-label="Legend">
      <div className="legend-head">
        <button className="link" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? 'Hide legend' : 'Legend'}
        </button>
        <button className="link" onClick={onReset}>
          Reset view
        </button>
      </div>
      {open && (
        <ul>
          <li>
            <span className="swatch allowed" aria-hidden="true" />
            Allowed
          </li>
          <li>
            <span className="swatch blocked" aria-hidden="true" />✕ Blocked
          </li>
          <li>
            <span className="swatch flagged" aria-hidden="true" />⚠ Flagged (ran)
          </li>
          <li>
            <span className="swatch external" aria-hidden="true" />
            Outside party
          </li>
          <li>
            <span className="swatch locked" aria-hidden="true" />
            Locked
          </li>
        </ul>
      )}
    </section>
  );
}
