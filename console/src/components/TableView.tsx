import { useMemo, useState } from 'react';
import type { Plane, RecEvent } from '../api/types';
import { isNoise, kindOf, reasonKey, shortName, type FlightKind } from '../model/flights';
import { clock } from '../model/format';
import { store, useStore } from '../state/store';
import { DecisionBadge, PlaneTag } from './common';

type Filter = 'all' | FlightKind;
const PAGE = 300;

export function filterEvents(events: RecEvent[], filter: Filter, plane: Plane | 'all', query: string, system: boolean): RecEvent[] {
  // Every word must match somewhere: "email attack" finds attack calls to email tools.
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const out: RecEvent[] = [];
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i]!;
    if (e.type !== 'decision' || !e.facts || !e.decision) continue;
    if (!system && isNoise(e)) continue;
    if (filter !== 'all' && kindOf(e) !== filter) continue;
    if (plane !== 'all' && e.facts.plane !== plane) continue;
    if (words.length) {
      const hay = [e.facts.target, reasonKey(e.decision), e.decision.reason, e.note?.label ?? '', e.facts.plane, ...e.facts.chain.map((p) => p.id)].join(' ').toLowerCase();
      if (!words.every((w) => hay.includes(w))) continue;
    }
    out.push(e);
  }
  return out;
}

export function TableView() {
  const events = useStore((s) => s.events);
  const selected = useStore((s) => s.selected);
  const [filter, setFilter] = useState<Filter>('all');
  const [plane, setPlane] = useState<Plane | 'all'>('all');
  const [query, setQuery] = useState('');
  const [system, setSystem] = useState(false);
  const [limit, setLimit] = useState(PAGE);

  const rows = useMemo(() => filterEvents(events, filter, plane, query, system), [events, filter, plane, query, system]);
  const counts = useMemo(() => {
    const c = { all: 0, allowed: 0, blocked: 0, flagged: 0 };
    for (const e of events) {
      if (e.type !== 'decision' || isNoise(e)) continue;
      c.all++;
      c[kindOf(e)]++;
    }
    return c;
  }, [events]);

  const open = (seq: number) => store.select({ kind: 'incident', seq });

  return (
    <div className="table-view">
      <h1 className="sr-only">All recorded calls</h1>
      <div className="toolbar" role="search">
        <label className="search">
          <span className="sr-only">Filter calls</span>
          <input type="search" placeholder="Filter by agent, tool, rule or label…" value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <div className="segmented" role="group" aria-label="Show">
          {(['all', 'blocked', 'flagged', 'allowed'] as Filter[]).map((f) => (
            <button key={f} aria-pressed={filter === f} className={filter === f ? 'on' : ''} onClick={() => setFilter(f)}>
              {f === 'all' ? 'All' : f[0]!.toUpperCase() + f.slice(1)} <span className="count">{counts[f]}</span>
            </button>
          ))}
        </div>
        <label className="select">
          <span>Kind</span>
          <select value={plane} onChange={(e) => setPlane(e.target.value as Plane | 'all')}>
            <option value="all">All</option>
            <option value="a2a">Agent calls</option>
            <option value="mcp">Tool calls</option>
            <option value="llm">Model calls</option>
          </select>
        </label>
        <label className="check">
          <input type="checkbox" checked={system} onChange={(e) => setSystem(e.target.checked)} /> Show gateway housekeeping (tool discovery)
        </label>
      </div>

      <div className="table-wrap">
        <table className="events">
          <caption className="sr-only">Recorded calls, newest first. Press Enter on a row to open its incident.</caption>
          <thead>
            <tr>
              <th scope="col">#</th>
              <th scope="col">Time</th>
              <th scope="col">Kind</th>
              <th scope="col">Chain</th>
              <th scope="col">Target</th>
              <th scope="col">Decision</th>
              <th scope="col">Why</th>
              <th scope="col">Label</th>
            </tr>
          </thead>
          <tbody>
            {rows.slice(0, limit).map((e) => {
              const f = e.facts!;
              const k = kindOf(e);
              const active = selected?.kind === 'incident' && selected.seq === e.seq;
              return (
                <tr
                  key={e.seq}
                  tabIndex={0}
                  className={`${k}${active ? ' active' : ''}`}
                  onClick={() => open(e.seq)}
                  onKeyDown={(ev) => {
                    if (ev.key === 'Enter' || ev.key === ' ') {
                      ev.preventDefault();
                      open(e.seq);
                    }
                  }}
                  aria-label={`Call ${e.seq}: ${f.actor.id} to ${f.target}, ${k}`}
                >
                  <td className="num">{e.seq}</td>
                  <td className="num">{clock(Date.parse(e.ts))}</td>
                  <td>
                    <PlaneTag plane={f.plane} />
                  </td>
                  <td className="chain">{f.chain.map((p) => shortName(p.id)).join(' › ')}</td>
                  <td>
                    <code>{f.target}</code>
                  </td>
                  <td>
                    <DecisionBadge d={e.decision!} monitored={k === 'flagged'} />
                  </td>
                  <td className="why">{e.decision!.effect === 'allow' ? <span className="muted">{e.decision!.code}</span> : e.decision!.reason}</td>
                  <td>{e.note?.label ? <span className={`label ${e.note.label}`}>{e.note.label}</span> : ''}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {rows.length === 0 && <p className="empty muted">No calls match.</p>}
        {rows.length > limit && (
          <button className="btn more" onClick={() => setLimit(limit + PAGE)}>
            Show {Math.min(PAGE, rows.length - limit)} more of {rows.length - limit}
          </button>
        )}
      </div>
    </div>
  );
}
