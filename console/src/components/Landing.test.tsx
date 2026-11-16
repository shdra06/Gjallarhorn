import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { store } from '../state/store';
import { Landing } from './Landing';

const connect = vi.fn(async (_token: string) => undefined);
vi.mock('../state/session', async (orig) => {
  const real = await orig<typeof import('../state/session')>();
  return {
    ...real,
    startDemo: async () => {
      const r = await fetch('/v1/demo-access');
      const b = (await r.json()) as { public_demo?: boolean; token?: string };
      if (!b.public_demo || !b.token) return false;
      await connect(b.token);
      return true;
    },
  };
});

beforeEach(() => store.reset());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  connect.mockClear();
});

describe('Landing', () => {
  it('starts the demo with one click on a public demo server', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"public_demo":true,"token":"demo-token"}', { status: 200 }));
    render(<Landing />);
    await act(async () => {
      fireEvent.click(screen.getAllByRole('button', { name: /Start the demo/ })[0]!);
    });
    expect(connect).toHaveBeenCalledWith('demo-token');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('asks for the token, and says where to find it, when the server does not share it', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"public_demo":false}', { status: 200 }));
    render(<Landing />);
    await act(async () => {
      fireEvent.click(screen.getAllByRole('button', { name: /Start the demo/ })[0]!);
    });
    expect(connect).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('./demo.sh');
    expect(screen.getByLabelText('Admin token')).toBeTruthy();
  });
});

describe('Theme', () => {
  it('switches and remembers the theme, and keeps it across sign-out', () => {
    render(<Landing />);
    fireEvent.click(screen.getByRole('button', { name: 'Dark theme' }));
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(localStorage.getItem('gj.theme')).toBe('dark');
    fireEvent.click(screen.getByRole('button', { name: 'Light theme' }));
    expect(document.documentElement.dataset.theme).toBe('light');
    store.reset();
    expect(store.get().theme).toBe('light');
    expect(getComputedStyle(document.documentElement).colorScheme ?? '').toBeDefined();
  });
});

describe('Signing in with a token, in the whole app', () => {
  it('keeps the form on screen and explains a wrong token', async () => {
    const { App } = await import('../App');
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"error":"missing or invalid admin token"}', { status: 401 }));
    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: /Sign in with a token/ }));
    fireEvent.change(screen.getByLabelText('Admin token'), { target: { value: 'wrong-token' } });
    await act(async () => {
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Sign in' }));
    });
    expect((await screen.findByRole('alert')).textContent).toContain('not accepted');
    expect(screen.getByRole('dialog').textContent).not.toContain("doesn't share");
  });
});
