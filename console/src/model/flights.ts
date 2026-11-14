import type { Decision, Kind, Plane, Principal, RecEvent } from '../api/types';

export type FlightKind = 'allowed' | 'blocked' | 'flagged';

/** One call, drawn as a flight from the caller to what it called. */
export interface Flight {
  seq: number;
  time: number;
  from: string;
  to: string;
  kind: FlightKind;
  plane: Plane;
  /** The rule ID or guard code that decided a denial. */
  key: string;
  actor: string;
  target: string;
  origin: string;
  chain: string[];
  label?: string;
  reason: string;
}

export type NodeRole = 'origin' | 'agent' | 'tool' | 'model';

export interface NodeInfo {
  id: string;
  label: string;
  role: NodeRole;
  kind: Kind | 'tool' | 'model';
  /** Started outside the company (trust=external), shown with a warning ring. */
  external: boolean;
  principal?: Principal;
}

/** Bifrost's own MCP discovery calls at startup: real, but not agent traffic. */
export function isNoise(e: RecEvent): boolean {
  const f = e.facts;
  return !!f && f.plane === 'mcp' && f.actor.kind === 'unknown' && /\.(list_tools|initialize)$/.test(f.target);
}

export function reasonKey(d: Decision | undefined): string {
  if (!d) return '';
  return d.rule_id || d.code;
}

export function kindOf(e: RecEvent): FlightKind {
  const d = e.decision;
  if (!d || d.effect === 'allow') return 'allowed';
  return e.note?.mode === 'monitor' ? 'flagged' : 'blocked';
}

/** The node a call lands on: an agent for A2A, a tool for MCP, a model for LLM calls. */
export function targetNode(plane: Plane, target: string): string {
  switch (plane) {
    case 'a2a':
      return target;
    case 'mcp':
      return `tool:${target}`;
    default:
      return `model:${target}`;
  }
}

export function toFlight(e: RecEvent): Flight | null {
  if (e.type !== 'decision' || !e.facts || !e.decision || isNoise(e)) return null;
  const f = e.facts;
  return {
    seq: e.seq,
    time: Date.parse(e.ts),
    from: f.actor.id,
    to: targetNode(f.plane, f.target),
    kind: kindOf(e),
    plane: f.plane,
    key: reasonKey(e.decision),
    actor: f.actor.id,
    target: f.target,
    origin: f.origin.id,
    chain: f.chain.map((p) => p.id),
    label: e.note?.label,
    reason: e.decision.reason,
  };
}

export function shortName(id: string): string {
  if (id.startsWith('tool:')) return id.slice(5);
  if (id.startsWith('model:')) {
    const m = id.slice(6);
    const i = m.indexOf('/');
    return i >= 0 ? m.slice(i + 1) : m;
  }
  const i = id.indexOf(':');
  return i >= 0 ? id.slice(i + 1) : id;
}

function principalNode(p: Principal): NodeInfo {
  return {
    id: p.id,
    label: shortName(p.id),
    role: p.kind === 'agent' ? 'agent' : 'origin',
    kind: p.kind,
    external: p.labels?.trust === 'external',
    principal: p,
  };
}

/**
 * All nodes: every registered principal, plus every principal, tool and model seen in
 * traffic. The insertion order is stable, so the layout doesn't jump around.
 */
export function collectNodes(principals: Principal[], events: Iterable<RecEvent>, into = new Map<string, NodeInfo>()): Map<string, NodeInfo> {
  for (const p of principals) {
    const existing = into.get(p.id);
    if (!existing || !existing.principal) into.set(p.id, principalNode(p));
  }
  for (const e of events) {
    if (e.type !== 'decision' || !e.facts || isNoise(e)) continue;
    const f = e.facts;
    for (const p of [f.actor, ...f.chain]) {
      if (!into.has(p.id)) into.set(p.id, principalNode(p));
    }
    const t = targetNode(f.plane, f.target);
    if (!into.has(t)) {
      if (f.plane === 'a2a') {
        into.set(t, { id: t, label: shortName(t), role: 'agent', kind: 'agent', external: false });
      } else {
        const role: NodeRole = f.plane === 'mcp' ? 'tool' : 'model';
        into.set(t, { id: t, label: shortName(t), role, kind: role, external: false });
      }
    }
  }
  return into;
}

export const kindIcon: Record<FlightKind, string> = { allowed: '✓', blocked: '✕', flagged: '⚠' };

export function decisionText(e: RecEvent): string {
  const k = kindOf(e);
  if (k === 'allowed') return 'Allowed';
  const key = reasonKey(e.decision);
  return k === 'flagged' ? `Flagged by ${key} (monitor-only, it ran)` : `Blocked by ${key}`;
}

export function planeName(p: Plane): string {
  return p === 'a2a' ? 'agent' : p === 'mcp' ? 'tool' : 'model';
}
