import type { Principal, RecEvent } from '../api/types';

export const chat: Principal = { id: 'service:customer-chat', kind: 'service', labels: { trust: 'external' } };
export const support: Principal = { id: 'agent:support', kind: 'agent' };
export const billing: Principal = { id: 'agent:billing', kind: 'agent' };
export const priya: Principal = { id: 'human:priya', kind: 'human', labels: { role: 'operator' } };

let seq = 0;
const base = Date.parse('2026-11-15T10:00:00Z');

export function resetSeq(): void {
  seq = 0;
}

export function decision(
  chain: Principal[],
  plane: 'llm' | 'mcp' | 'a2a',
  target: string,
  effect: 'allow' | 'deny' = 'allow',
  opts: { rule?: string; code?: string; monitor?: boolean; label?: string; at?: number } = {},
): RecEvent {
  seq++;
  const actor = chain[chain.length - 1]!;
  const ts = new Date(opts.at ?? base + seq * 1000).toISOString();
  return {
    seq,
    ts,
    type: 'decision',
    facts: { time: ts, plane, target, actor, origin: chain[0]!, chain, depth: chain.length - 1, credential: actor },
    decision: {
      effect,
      code: opts.code ?? (effect === 'deny' ? 'rule' : 'default'),
      rule_id: opts.rule,
      reason: effect === 'deny' ? 'Refunds only when a human operator started the chain' : 'no rule matched',
    },
    note: { ...(opts.label ? { label: opts.label } : {}), ...(opts.monitor ? { mode: 'monitor' } : {}) },
  };
}

export function noteEvent(type: string, note: Record<string, string>): RecEvent {
  seq++;
  return { seq, ts: new Date(base + seq * 1000).toISOString(), type, note };
}

/** The borrowed-refund attack: chat → support → billing → billing.refund (blocked). */
export function borrowedRefund(): RecEvent[] {
  return [
    decision([chat], 'a2a', 'agent:support', 'allow', { label: 'attack' }),
    decision([chat, support], 'llm', 'mock/standard-model', 'allow', { label: 'attack' }),
    decision([chat, support], 'a2a', 'agent:billing', 'allow', { label: 'attack' }),
    decision([chat, support, billing], 'mcp', 'billing.refund', 'deny', { rule: 'refunds-need-an-operator', label: 'attack' }),
  ];
}
