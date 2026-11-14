import { useMemo } from 'react';
import { kindIcon, kindOf, reasonKey, shortName, targetNode, type NodeInfo } from '../model/flights';
import { timeAgo } from '../model/format';
import { useNow } from '../hooks';
import { isLocked, store, useStore } from '../state/store';
import { lockPrincipal, unlockPrincipal } from './common';

const roleText: Record<NodeInfo['role'], string> = { origin: 'Starts work', agent: 'Agent', tool: 'Tool', model: 'Model' };

export function NodePanel({ id }: { id: string }) {
  const node = useStore((s) => s.nodes.get(id));
  const lockdowns = useStore((s) => s.lockdowns);
  const events = useStore((s) => s.events);
  const now = useNow(5000);
  const lock = lockdowns.find((l) => l.principal === id);

  const { recent, out, inn } = useMemo(() => {
    const recent = [];
    const out = { allowed: 0, blocked: 0, flagged: 0 };
    const inn = { allowed: 0, blocked: 0, flagged: 0 };
    for (let i = events.length - 1; i >= 0; i--) {
      const e = events[i]!;
      if (e.type !== 'decision' || !e.facts) continue;
      const from = e.facts.actor.id === id;
      const to = targetNode(e.facts.plane, e.facts.target) === id;
      if (!from && !to) continue;
      const k = kindOf(e);
      (from ? out : inn)[k]++;
      if (recent.length < 12) recent.push(e);
    }
    return { recent, out, inn };
  }, [events, id]);

  if (!node) return <p className="panel">Unknown node.</p>;
  const lockable = !!node.principal;

  return (
    <div className="panel node">
      <p className="eyebrow">{roleText[node.role]}</p>
      <h2 tabIndex={-1}>{node.id}</h2>
      <div className="node-tags">
        {node.principal && <span className="badge neutral">{node.principal.kind}</span>}
        {node.external && <span className="badge external">outside party</span>}
        {Object.entries(node.principal?.labels ?? {}).map(([k, v]) => (
          <span key={k} className="badge neutral">
            {k}={v}
          </span>
        ))}
      </div>

      {lockable && (
        <div className={`lock-box ${lock ? 'locked' : ''}`}>
          {lock ? (
            <>
              <p>
                <strong>🔒 Locked</strong> {lock.auto ? `automatically by rule ${lock.rule_id}` : `by ${lock.by}`}, {timeAgo(Date.parse(lock.since), now)}.
              </p>
              <p className="small">{lock.reason}</p>
              <button className="btn" onClick={() => unlockPrincipal(id)}>
                Unlock {shortName(id)}
              </button>
            </>
          ) : (
            <>
              <p className="small">Locking cuts {shortName(id)} off from models, tools and other agents on its next call, without touching anyone else's access.</p>
              <button className="btn danger big" onClick={() => lockPrincipal(id)}>
                <span aria-hidden="true">⏻</span> Lock {shortName(id)}
              </button>
            </>
          )}
        </div>
      )}

      <h3>Traffic in loaded history</h3>
      <table className="mini">
        <thead>
          <tr>
            <th scope="col" />
            <th scope="col">Allowed</th>
            <th scope="col">Blocked</th>
            <th scope="col">Flagged</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <th scope="row">Calls it made</th>
            <td>{out.allowed}</td>
            <td>{out.blocked}</td>
            <td>{out.flagged}</td>
          </tr>
          <tr>
            <th scope="row">Calls to it</th>
            <td>{inn.allowed}</td>
            <td>{inn.blocked}</td>
            <td>{inn.flagged}</td>
          </tr>
        </tbody>
      </table>

      <h3>Recent calls</h3>
      {recent.length === 0 ? (
        <p className="muted small">No calls recorded yet.</p>
      ) : (
        <ul className="recent">
          {recent.map((e) => {
            const k = kindOf(e);
            return (
              <li key={e.seq}>
                <button className={`recent-item ${k}`} onClick={() => store.select({ kind: 'incident', seq: e.seq })}>
                  <span aria-hidden="true">{kindIcon[k]}</span> {shortName(e.facts!.actor.id)} → {e.facts!.target}
                  {k !== 'allowed' && <span className="muted"> · {reasonKey(e.decision)}</span>}
                  <span className="muted small"> · {timeAgo(Date.parse(e.ts), now)}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {isLocked(lockdowns, id) && <p className="sr-only">This principal is locked.</p>}
    </div>
  );
}
