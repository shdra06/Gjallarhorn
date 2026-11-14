import { beforeEach, describe, expect, it } from 'vitest';
import { borrowedRefund, chat, decision, priya, resetSeq, support } from '../test/fixtures';
import { collectNodes, decisionText, isNoise, kindOf, shortName, targetNode, toFlight } from './flights';
import { describeMatch, timeAgo } from './format';
import { columns, layout } from './layout';

beforeEach(resetSeq);

describe('flights', () => {
  it('turns decisions into flights from the caller to what it called', () => {
    const [toSupport, think, , refund] = borrowedRefund();
    expect(toFlight(toSupport!)).toMatchObject({ from: 'service:customer-chat', to: 'agent:support', kind: 'allowed', plane: 'a2a' });
    expect(toFlight(think!)).toMatchObject({ from: 'agent:support', to: 'model:mock/standard-model' });
    expect(toFlight(refund!)).toMatchObject({
      from: 'agent:billing',
      to: 'tool:billing.refund',
      kind: 'blocked',
      key: 'refunds-need-an-operator',
      chain: ['service:customer-chat', 'agent:support', 'agent:billing'],
    });
  });

  it('marks calls that ran in monitor-only mode as flagged', () => {
    const e = decision([chat, support], 'mcp', 'email.send', 'deny', { rule: 'r', monitor: true });
    expect(kindOf(e)).toBe('flagged');
    expect(decisionText(e)).toContain('monitor-only');
  });

  it('leaves out Bifrost tool discovery but keeps everything else', () => {
    const discovery = decision([{ id: 'unknown:anonymous', kind: 'unknown' }], 'mcp', 'billing.list_tools');
    expect(isNoise(discovery)).toBe(true);
    expect(toFlight(discovery)).toBeNull();
    expect(isNoise(decision([priya], 'mcp', 'billing.list_tools'))).toBe(false);
  });

  it('names nodes for people, agents, tools and models', () => {
    expect(targetNode('mcp', 'email.send')).toBe('tool:email.send');
    expect(shortName('agent:support')).toBe('support');
    expect(shortName('tool:email.send')).toBe('email.send');
    expect(shortName('model:mock/standard-model')).toBe('standard-model');
  });

  it('collects nodes from principals and traffic, in roles', () => {
    const nodes = collectNodes([priya], borrowedRefund());
    expect(nodes.get('human:priya')?.role).toBe('origin');
    expect(nodes.get('service:customer-chat')).toMatchObject({ role: 'origin', external: true });
    expect(nodes.get('agent:billing')?.role).toBe('agent');
    expect(nodes.get('tool:billing.refund')?.role).toBe('tool');
    expect(nodes.get('model:mock/standard-model')?.role).toBe('model');
  });
});

describe('layout', () => {
  it('puts each role in its column, without overlaps', () => {
    const nodes = collectNodes([priya], borrowedRefund());
    const pos = layout(nodes.values());
    expect(pos.size).toBe(nodes.size);
    const agentXs = ['agent:support', 'agent:billing'].map((id) => pos.get(id)!.x);
    for (const x of agentXs) expect(Math.abs(x - columns.agent.x)).toBeLessThan(3);
    expect(pos.get('human:priya')!.x).toBeLessThan(pos.get('agent:support')!.x);
    expect(pos.get('tool:billing.refund')!.x).toBeGreaterThan(pos.get('agent:support')!.x);
    const seen = new Set<string>();
    for (const p of pos.values()) {
      const k = `${Math.round(p.x)},${Math.round(p.z)}`;
      expect(seen.has(k)).toBe(false);
      seen.add(k);
    }
  });
});

describe('format', () => {
  it('describes a policy match in words', () => {
    expect(describeMatch({ targets: ['email.send'], actors: ['agent:research'], origin_labels: { trust: 'external' }, delegated: true })).toEqual([
      'target email.send',
      'actor agent:research',
      'started by label trust=external',
      'delegated',
    ]);
  });

  it('says how long ago', () => {
    const now = Date.parse('2026-11-15T10:00:00Z');
    expect(timeAgo(now - 2000, now)).toBe('just now');
    expect(timeAgo(now - 30_000, now)).toBe('30s ago');
    expect(timeAgo(now - 5 * 60_000, now)).toBe('5 min ago');
  });
});
