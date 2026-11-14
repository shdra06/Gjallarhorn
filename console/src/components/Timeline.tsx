import { useEffect, useMemo, useRef } from 'react';
import { isNoise, kindOf } from '../model/flights';
import { clock } from '../model/format';
import { store, useStore } from '../state/store';

const BUCKETS = 120;

/** Histogram of recorded calls with a replay cursor: drag back in time, press play. */
export function Timeline() {
  const events = useStore((s) => s.events);
  const replay = useStore((s) => s.replay);
  const canvas = useRef<HTMLCanvasElement>(null);

  const span = useMemo(() => {
    const decisions = events.filter((e) => e.type === 'decision' && !isNoise(e));
    if (!decisions.length) return null;
    const t0 = Date.parse(decisions[0]!.ts);
    const t1 = Math.max(t0 + 1000, Date.parse(decisions[decisions.length - 1]!.ts));
    const ok = new Array<number>(BUCKETS).fill(0);
    const bad = new Array<number>(BUCKETS).fill(0);
    for (const e of decisions) {
      const i = Math.min(BUCKETS - 1, Math.floor(((Date.parse(e.ts) - t0) / (t1 - t0)) * BUCKETS));
      if (kindOf(e) === 'allowed') ok[i]!++;
      else bad[i]!++;
    }
    return { t0, t1, ok, bad, max: Math.max(1, ...ok.map((v, i) => v + bad[i]!)) };
  }, [events]);

  useEffect(() => {
    const c = canvas.current;
    if (!c || !span) return;
    const dpr = window.devicePixelRatio || 1;
    const w = c.clientWidth;
    const h = c.clientHeight;
    c.width = w * dpr;
    c.height = h * dpr;
    const g = c.getContext('2d');
    if (!g) return;
    g.scale(dpr, dpr);
    g.clearRect(0, 0, w, h);
    const bw = w / BUCKETS;
    for (let i = 0; i < BUCKETS; i++) {
      const okH = (span.ok[i]! / span.max) * (h - 4);
      const badH = (span.bad[i]! / span.max) * (h - 4);
      g.fillStyle = 'rgba(56,225,255,0.55)';
      g.fillRect(i * bw + 0.5, h - okH, Math.max(1, bw - 1), okH);
      g.fillStyle = 'rgba(255,59,92,0.95)';
      g.fillRect(i * bw + 0.5, h - okH - badH, Math.max(1, bw - 1), badH);
    }
  }, [span]);

  if (!span) {
    return (
      <section className="timeline empty" aria-label="Replay timeline">
        <span className="muted small">The replay timeline fills in as calls are recorded.</span>
      </section>
    );
  }

  const value = replay.active ? replay.cursor : span.t1;
  const pct = ((value - span.t0) / (span.t1 - span.t0)) * 100;
  const seek = (v: number) => store.set((s) => ({ replay: { ...s.replay, active: true, playing: false, cursor: v } }));
  const play = () =>
    store.set((s) => {
      const start = s.replay.active ? s.replay.cursor : Math.max(span.t0, span.t1 - 60_000);
      return { replay: { ...s.replay, active: true, playing: !s.replay.playing || !s.replay.active, cursor: start } };
    });
  const live = () => store.set((s) => ({ replay: { ...s.replay, active: false, playing: false } }));

  return (
    <section className="timeline" aria-label="Replay timeline">
      <button className={`btn small ${replay.active ? '' : 'live-on'}`} onClick={live} aria-pressed={!replay.active}>
        <span className="dot" aria-hidden="true" /> Live
      </button>
      <button className="btn small" onClick={play} aria-label={replay.playing ? 'Pause replay' : replay.active ? 'Play replay' : 'Replay the last minute'}>
        {replay.playing ? '❚❚' : '▶'}
        <span className="btn-text">{replay.active ? '' : ' Replay last minute'}</span>
      </button>
      <label className="speed">
        <span className="sr-only">Replay speed</span>
        <select value={replay.speed} onChange={(e) => store.set((s) => ({ replay: { ...s.replay, speed: Number(e.target.value) } }))}>
          {[1, 4, 16, 64].map((v) => (
            <option key={v} value={v}>
              {v}×
            </option>
          ))}
        </select>
      </label>
      <div className="track-wrap">
        <canvas ref={canvas} className="histogram" aria-hidden="true" />
        <div className="cursor" style={{ left: `${pct}%` }} aria-hidden="true" />
        <input
          type="range"
          className="scrub"
          min={span.t0}
          max={span.t1}
          step={100}
          value={value}
          onChange={(e) => seek(Number(e.target.value))}
          aria-label="Replay position"
          aria-valuetext={`${clock(value)}${replay.active ? '' : ', live'}`}
        />
      </div>
      <span className="time small">{replay.active ? clock(value) : 'now'}</span>
    </section>
  );
}
