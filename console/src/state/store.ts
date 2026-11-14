import { useRef, useSyncExternalStore } from 'react';
import type { CopilotInfo, Lockdown, Principal, RecEvent } from '../api/types';
import { collectNodes, type NodeInfo } from '../model/flights';

export type View = 'airspace' | 'table' | 'policy';
export type Conn = 'idle' | 'connecting' | 'live' | 'offline' | 'unauthorized';
export type Selection = { kind: 'incident'; seq: number } | { kind: 'node'; id: string } | null;
export type Tone = 'info' | 'good' | 'bad' | 'warn';

export interface Toast {
  id: number;
  tone: Tone;
  title: string;
  body?: string;
  action?: { label: string; run: () => void };
}

export interface Replay {
  /** True while the airspace shows recorded traffic instead of live traffic. */
  active: boolean;
  /** The replay position (event time, ms). */
  cursor: number;
  playing: boolean;
  speed: number;
}

export interface State {
  conn: Conn;
  events: RecEvent[];
  /** Bumped on every change to events, so views can memoise on it. */
  version: number;
  principals: Principal[];
  nodes: Map<string, NodeInfo>;
  lockdowns: Lockdown[];
  enforcing: boolean | null;
  view: View;
  selected: Selection;
  reducedMotion: boolean;
  toasts: Toast[];
  demo: boolean;
  copilot: CopilotInfo | null;
  replay: Replay;
  helpOpen: boolean;
}

export const MAX_EVENTS = 20000;

function prefersReducedMotion(): boolean {
  try {
    const saved = localStorage.getItem('gj.reducedMotion');
    if (saved !== null) return saved === '1';
  } catch {
    // Storage can be blocked; fall back to the system setting.
  }
  return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
}

export function initialState(): State {
  return {
    conn: 'idle',
    events: [],
    version: 0,
    principals: [],
    nodes: new Map(),
    lockdowns: [],
    enforcing: null,
    view: 'airspace',
    selected: null,
    reducedMotion: prefersReducedMotion(),
    toasts: [],
    demo: false,
    copilot: null,
    replay: { active: false, cursor: 0, playing: false, speed: 4 },
    helpOpen: false,
  };
}

type Listener = () => void;

/** A small external store: one state object, replaced on every change. */
export class Store {
  private state: State;
  private listeners = new Set<Listener>();
  private live = new Set<(e: RecEvent) => void>();
  private toastId = 0;

  constructor(state: State = initialState()) {
    this.state = state;
  }

  get = (): State => this.state;

  set(patch: Partial<State> | ((s: State) => Partial<State>)): void {
    const p = typeof patch === 'function' ? patch(this.state) : patch;
    this.state = { ...this.state, ...p };
    for (const l of this.listeners) l();
  }

  subscribe = (l: Listener): (() => void) => {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  };

  /** Called for each event as it arrives live (not for the backlog). */
  onLive(cb: (e: RecEvent) => void): () => void {
    this.live.add(cb);
    return () => this.live.delete(cb);
  }

  lastSeq(): number {
    const ev = this.state.events;
    return ev.length ? ev[ev.length - 1]!.seq : 0;
  }

  /** Adds events in order, skipping any already seen. */
  addEvents(batch: RecEvent[], live: boolean): void {
    let last = this.lastSeq();
    const fresh = batch.filter((e) => {
      if (e.seq <= last) return false;
      last = e.seq;
      return true;
    });
    if (!fresh.length) return;
    let events = this.state.events.concat(fresh);
    if (events.length > MAX_EVENTS) events = events.slice(events.length - MAX_EVENTS);
    const nodes = collectNodes(this.state.principals, fresh, new Map(this.state.nodes));
    let { enforcing, lockdowns } = this.state;
    for (const e of fresh) {
      if (e.type === 'policy' && e.note?.mode) enforcing = e.note.mode === 'enforce';
      if (e.type === 'lockdown' && e.note?.principal) {
        const n = e.note;
        lockdowns = lockdowns.filter((l) => l.principal !== n.principal).concat({
          principal: n.principal!,
          reason: n.reason ?? '',
          by: n.by ?? '',
          since: e.ts,
          auto: n.auto === 'true',
          rule_id: n.rule_id,
        });
      }
      if (e.type === 'unlock' && e.note?.principal) {
        lockdowns = lockdowns.filter((l) => l.principal !== e.note!.principal);
      }
    }
    this.set((s) => ({ events, nodes, enforcing, lockdowns, version: s.version + 1 }));
    if (live) for (const e of fresh) for (const cb of this.live) cb(e);
  }

  setPrincipals(principals: Principal[]): void {
    this.set((s) => ({ principals, nodes: collectNodes(principals, s.events, new Map(s.nodes)) }));
  }

  toast(tone: Tone, title: string, body?: string, action?: Toast['action'], ms = 6000): number {
    const id = ++this.toastId;
    this.set((s) => ({ toasts: [...s.toasts.slice(-3), { id, tone, title, body, action }] }));
    if (ms > 0) setTimeout(() => this.dismiss(id), ms);
    return id;
  }

  dismiss(id: number): void {
    this.set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }));
  }

  select(selected: Selection): void {
    this.set({ selected });
  }

  setReducedMotion(on: boolean): void {
    try {
      localStorage.setItem('gj.reducedMotion', on ? '1' : '0');
    } catch {
      // Not saved; it still applies for this visit.
    }
    this.set({ reducedMotion: on });
  }

  reset(): void {
    this.state = { ...initialState(), reducedMotion: this.state.reducedMotion };
    for (const l of this.listeners) l();
  }
}

export const store = new Store();

/** Subscribes a component to a slice of the store. Re-renders only when the slice changes. */
export function useStore<T>(select: (s: State) => T, equal: (a: T, b: T) => boolean = Object.is): T {
  const cache = useRef<{ state: State; value: T } | null>(null);
  const get = () => {
    const s = store.get();
    const c = cache.current;
    if (c && c.state === s) return c.value;
    const v = select(s);
    if (c && equal(c.value, v)) {
      cache.current = { state: s, value: c.value };
      return c.value;
    }
    cache.current = { state: s, value: v };
    return v;
  };
  return useSyncExternalStore(store.subscribe, get, get);
}

export function isLocked(lockdowns: Lockdown[], id: string): boolean {
  return lockdowns.some((l) => l.principal === id);
}
