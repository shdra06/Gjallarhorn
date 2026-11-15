import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Api } from '../api/client';
import { store } from '../state/store';
import { DemoPanel } from './DemoPanel';

vi.mock('../state/session', () => ({
  act: async <T,>(_what: string, fn: (a: Api) => Promise<T>) => fn(new Api('t')),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  store.reset();
});

const ids = ['borrowed-refund', 'refund-with-operator-key', 'multi-step-injection', 'data-exfiltration', 'forged-chain-at-entry', 'locked-agent-unlocks-itself'];

describe('DemoPanel', () => {
  it('shows the main attacks first and the whole catalogue on request', async () => {
    const attacks = Array.from({ length: 31 }, (_, i) => ({ id: ids[i] ?? `attack-${i}`, label: 'attack', title: `Attack ${i}`, story: `Story ${i}` }));
    const benign = [{ id: 'order-status', label: 'benign', title: 'Order status', story: '' }];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      const body = url.endsWith('/demo/scenarios') ? [...attacks, ...benign] : { on: false, runs: 0 };
      return new Response(JSON.stringify(body), { status: 200 });
    });
    store.set({ demo: true });
    render(<DemoPanel />);
    const more = await screen.findByRole('button', { name: 'Show all 31 attacks' });
    expect(screen.getAllByRole('button', { name: /^🚀|Attack/ }).filter((b) => b.className === 'attack-btn')).toHaveLength(6);
    fireEvent.click(more);
    expect(screen.getAllByRole('button').filter((b) => b.className === 'attack-btn')).toHaveLength(31);
    expect(screen.queryByText('Order status')).toBeNull();
  });
});
