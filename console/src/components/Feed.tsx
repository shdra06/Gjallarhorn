import { useMemo } from 'react';
import { kindIcon, kindOf, reasonKey, shortName } from '../model/flights';
import { timeAgo } from '../model/format';
import { useNow } from '../hooks';
import { store, useStore } from '../state/store';

/** The latest calls Gjallarhorn stopped or flagged; each opens its incident. */
export function Feed() {
  const events = useStore((s) => s.events);
  const selected = useStore((s) => s.selected);
  const now = useNow(5000);
  const items = useMemo(() => {
    const out = [];
    for (let i = events.length - 1; i >= 0 && out.length < 8; i--) {
      const e = events[i]!;
      if (e.type === 'decision' && e.facts && kindOf(e) !== 'allowed') out.push(e);
    }
    return out;
  }, [events]);

  return (
    <section className="hud feed" aria-labelledby="feed-title" data-tour="feed">
      <h2 id="feed-title">Interceptions</h2>
      {items.length === 0 ? (
        <p className="muted small">Nothing stopped yet. When Gjallarhorn blocks a call, it shows up here and as a red ✕ in the sky.</p>
      ) : (
        <ol>
          {items.map((e) => {
            const k = kindOf(e);
            const f = e.facts!;
            const chain = f.chain.map((p) => shortName(p.id)).join(' › ');
            const active = selected?.kind === 'incident' && selected.seq === e.seq;
            return (
              <li key={e.seq}>
                <button className={`feed-item ${k}${active ? ' active' : ''}`} onClick={() => store.select({ kind: 'incident', seq: e.seq })}>
                  <span className="feed-key">
                    <span aria-hidden="true">{kindIcon[k]}</span> {reasonKey(e.decision)}
                  </span>
                  <span className="feed-route">
                    {chain} → {f.target}
                  </span>
                  <span className="feed-time">
                    {k === 'flagged' ? 'flagged, ran · ' : 'blocked · '}
                    {timeAgo(Date.parse(e.ts), now)}
                  </span>
                </button>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
