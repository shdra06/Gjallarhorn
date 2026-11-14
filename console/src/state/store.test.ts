import { beforeEach, describe, expect, it } from 'vitest';
import { borrowedRefund, noteEvent, resetSeq } from '../test/fixtures';
import { MAX_EVENTS, Store, initialState } from './store';

beforeEach(resetSeq);

describe('Store', () => {
  it('adds events in order, skipping ones already seen', () => {
    const s = new Store(initialState());
    const ev = borrowedRefund();
    s.addEvents(ev.slice(0, 2), false);
    s.addEvents(ev, false);
    expect(s.get().events.map((e) => e.seq)).toEqual([1, 2, 3, 4]);
    expect(s.lastSeq()).toBe(4);
    expect(s.get().nodes.has('tool:billing.refund')).toBe(true);
  });

  it('tells live listeners about new events only', () => {
    const s = new Store(initialState());
    const seen: number[] = [];
    s.onLive((e) => seen.push(e.seq));
    const ev = borrowedRefund();
    s.addEvents(ev.slice(0, 2), false);
    s.addEvents(ev.slice(1), true);
    expect(seen).toEqual([3, 4]);
  });

  it('follows lockdowns, unlocks and mode changes from the stream', () => {
    const s = new Store(initialState());
    s.addEvents([noteEvent('lockdown', { principal: 'agent:research', reason: 'shell', by: 'rule:agents-never-run-shell', auto: 'true', rule_id: 'agents-never-run-shell' })], true);
    expect(s.get().lockdowns).toMatchObject([{ principal: 'agent:research', auto: true, rule_id: 'agents-never-run-shell' }]);
    s.addEvents([noteEvent('policy', { mode: 'monitor', by: 'console' })], true);
    expect(s.get().enforcing).toBe(false);
    s.addEvents([noteEvent('unlock', { principal: 'agent:research', by: 'priya' })], true);
    expect(s.get().lockdowns).toEqual([]);
  });

  it('keeps at most MAX_EVENTS events', () => {
    const s = new Store(initialState());
    const many = Array.from({ length: MAX_EVENTS + 10 }, (_, i) => ({ seq: i + 1, ts: new Date(0).toISOString(), type: 'outcome' }));
    s.addEvents(many, false);
    expect(s.get().events.length).toBe(MAX_EVENTS);
    expect(s.get().events[0]!.seq).toBe(11);
  });

  it('keeps a few toasts and dismisses them', () => {
    const s = new Store(initialState());
    for (let i = 0; i < 6; i++) s.toast('info', `t${i}`, undefined, undefined, 0);
    expect(s.get().toasts.map((t) => t.title)).toEqual(['t2', 't3', 't4', 't5']);
    s.dismiss(s.get().toasts[0]!.id);
    expect(s.get().toasts.length).toBe(3);
  });
});
