import { useEffect, useMemo, useRef, useState } from 'react';
import { targetNode, toFlight } from '../model/flights';
import { store, useStore } from '../state/store';
import type { Airspace } from '../three/Airspace';
import { webglAvailable } from '../three/webgl';
import { DemoPanel } from './DemoPanel';
import { Feed } from './Feed';
import { Legend, StatsHud } from './Hud';
import { Timeline } from './Timeline';

export function AirspaceView() {
  const host = useRef<HTMLDivElement>(null);
  const space = useRef<Airspace | null>(null);
  const [failed, setFailed] = useState(() => !webglAvailable());
  const [ready, setReady] = useState(false);
  const nodes = useStore((s) => s.nodes);
  const lockdowns = useStore((s) => s.lockdowns);
  const reduced = useStore((s) => s.reducedMotion);
  const selected = useStore((s) => s.selected);
  const replay = useStore((s) => s.replay);
  const theme = useStore((s) => s.theme);

  // Three.js is loaded on demand, so signing in and the other views don't wait for it.
  useEffect(() => {
    if (failed || !host.current) return;
    let gone = false;
    let off = () => {};
    import('../three/Airspace')
      .then(({ Airspace }) => {
        if (gone || !host.current) return;
        space.current = new Airspace(host.current, {
          reducedMotion: store.get().reducedMotion,
          theme,
          onSelectNode: (id) => store.select({ kind: 'node', id }),
          onSelectFlight: (seq) => store.select({ kind: 'incident', seq }),
        });
        off = store.onLive((e) => {
          if (store.get().replay.active) return;
          const f = toFlight(e);
          if (f) space.current?.addFlight(f);
        });
        setReady(true);
      })
      .catch(() => setFailed(true));
    return () => {
      gone = true;
      off();
      space.current?.dispose();
      space.current = null;
      setReady(false);
    };
    // A new theme rebuilds the scene with its own colours.
  }, [failed, theme]);

  useEffect(() => {
    const s = space.current;
    if (!s) return;
    s.setNodes([...nodes.values()], new Set(lockdowns.map((l) => l.principal)));
    const events = store.get().events;
    const flights = [];
    for (let i = Math.max(0, events.length - 2000); i < events.length; i++) {
      const f = toFlight(events[i]!);
      if (f) flights.push(f);
    }
    s.seedCorridors(flights);
  }, [nodes, lockdowns, ready]);

  useEffect(() => space.current?.setReducedMotion(reduced), [reduced, ready]);

  // Highlight the chain of the selected call, or everything a selected node touched.
  const route = useMemo(() => {
    if (!selected) return null;
    if (selected.kind === 'node') return [selected.id];
    const e = store.get().events.find((x) => x.seq === selected.seq);
    if (!e?.facts) return null;
    return [...e.facts.chain.map((p) => p.id), targetNode(e.facts.plane, e.facts.target)];
  }, [selected]);
  useEffect(() => space.current?.setHighlight(route), [route, ready]);

  // Replay: feed recorded calls to the airspace as the cursor moves.
  const lastCursor = useRef(0);
  useEffect(() => {
    space.current?.clearFlights();
    lastCursor.current = replay.cursor;
  }, [replay.active]);
  useEffect(() => {
    if (!replay.active || !replay.playing) return;
    lastCursor.current = store.get().replay.cursor;
    const tick = 100;
    const t = setInterval(() => {
      const s = store.get();
      const events = s.events;
      const end = events.length ? Date.parse(events[events.length - 1]!.ts) : 0;
      const next = s.replay.cursor + tick * s.replay.speed;
      for (const e of events) {
        const ts = Date.parse(e.ts);
        if (ts > lastCursor.current && ts <= next) {
          const f = toFlight(e);
          if (f) space.current?.addFlight(f);
        }
      }
      lastCursor.current = next;
      if (next >= end) {
        store.set({ replay: { ...s.replay, active: false, playing: false, cursor: end } });
        store.toast('info', 'Replay finished', 'Back to live traffic.');
      } else {
        store.set({ replay: { ...s.replay, cursor: next } });
      }
    }, tick);
    return () => clearInterval(t);
  }, [replay.active, replay.playing]);

  if (failed) {
    return (
      <div className="fallback">
        <h2>The 3D airspace needs WebGL</h2>
        <p>This browser can't draw it. Everything it shows is also in the table view.</p>
        <button className="btn primary" onClick={() => store.set({ view: 'table' })}>
          Open the table view
        </button>
      </div>
    );
  }

  return (
    <div className="airspace">
      <div className="airspace-host" ref={host} />
      <h1 className="sr-only">Airspace: live calls between people, apps, agents, tools and models</h1>
      <StatsHud />
      <Feed />
      {replay.active && (
        <div className="replay-banner" role="status">
          <span aria-hidden="true">⏪</span> Replaying recorded traffic
        </div>
      )}
      <div className="hud-bottom-left">
        <DemoPanel />
        <Legend onReset={() => space.current?.resetView()} />
      </div>
      <Timeline />
    </div>
  );
}
