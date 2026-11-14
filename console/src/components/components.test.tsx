import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { borrowedRefund, decision, priya, resetSeq } from '../test/fixtures';
import { store } from '../state/store';
import { Feed } from './Feed';
import { Login } from './Login';
import { TableView, filterEvents } from './TableView';
import { DecisionBadge, Route } from './common';

beforeEach(() => {
  resetSeq();
  store.reset();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('DecisionBadge and Route', () => {
  it('say blocked, flagged or allowed in words and icons, not colour alone', () => {
    render(
      <>
        <DecisionBadge d={{ effect: 'deny', code: 'rule', rule_id: 'r1', reason: 'x' }} />
        <DecisionBadge d={{ effect: 'deny', code: 'rule', rule_id: 'r2', reason: 'x' }} monitored />
        <DecisionBadge d={{ effect: 'allow', code: 'default', reason: 'x' }} />
      </>,
    );
    expect(screen.getByText(/Blocked · r1/)).toBeTruthy();
    expect(screen.getByText(/Flagged · r2/)).toBeTruthy();
    expect(screen.getByText(/Allowed/)).toBeTruthy();
  });

  it('shows the chain as a list', () => {
    render(<Route chain={['service:customer-chat', 'agent:support']} target="billing.refund" stopped />);
    const items = screen.getAllByRole('listitem').map((li) => li.textContent);
    expect(items).toEqual(['customer-chat', 'support', '✕ billing.refund']);
  });
});

describe('Feed', () => {
  it('lists stopped calls and opens one', () => {
    store.addEvents(borrowedRefund(), false);
    render(<Feed />);
    const btn = screen.getByRole('button', { name: /refunds-need-an-operator/ });
    expect(btn.textContent).toContain('customer-chat › support › billing → billing.refund');
    fireEvent.click(btn);
    expect(store.get().selected).toEqual({ kind: 'incident', seq: 4 });
  });
});

describe('TableView', () => {
  it('filters by decision, kind and text', () => {
    const ev = [...borrowedRefund(), decision([priya], 'mcp', 'billing.refund', 'allow', { label: 'benign' })];
    expect(filterEvents(ev, 'blocked', 'all', '', false).map((e) => e.seq)).toEqual([4]);
    expect(filterEvents(ev, 'all', 'a2a', '', false).length).toBe(2);
    expect(filterEvents(ev, 'all', 'all', 'priya', false).map((e) => e.seq)).toEqual([5]);
    expect(filterEvents(ev, 'all', 'all', 'refunds-need', false).map((e) => e.seq)).toEqual([4]);
    expect(filterEvents(ev, 'all', 'all', 'billing.refund  attack', false).map((e) => e.seq)).toEqual([4]);
    expect(filterEvents(ev, 'all', 'all', 'billing.refund benign', false).map((e) => e.seq)).toEqual([5]);
  });

  it('opens a row with the keyboard', () => {
    store.addEvents(borrowedRefund(), false);
    render(<TableView />);
    const row = screen.getByRole('row', { name: /Call 4:/ });
    fireEvent.keyDown(row, { key: 'Enter' });
    expect(store.get().selected).toEqual({ kind: 'incident', seq: 4 });
    fireEvent.click(screen.getByRole('button', { name: /Blocked/ }));
    expect(screen.getAllByRole('row').length).toBe(2); // header + the one blocked call
  });
});

describe('Login', () => {
  it('explains a rejected token', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"error":"missing or invalid admin token"}', { status: 401 }));
    render(<Login />);
    fireEvent.change(screen.getByLabelText('Admin token'), { target: { value: 'wrong-token' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    });
    expect((await screen.findByRole('alert')).textContent).toContain('not accepted');
    const [, init] = (globalThis.fetch as unknown as { mock: { calls: [string, RequestInit][] } }).mock.calls[0]!;
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer wrong-token');
  });
});
