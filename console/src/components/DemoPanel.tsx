import { useEffect, useState } from 'react';
import type { Scenario, ScenarioResult } from '../api/types';
import { act } from '../state/session';
import { store, useStore } from '../state/store';

function describe(r: ScenarioResult): { tone: 'good' | 'bad' | 'warn' | 'info'; title: string; body: string } {
  const effects = Object.entries(r.effects ?? {})
    .map(([k, v]) => `${k} ×${v}`)
    .join(', ');
  const denials = [...new Set(r.denials ?? [])].join(', ');
  if (r.error) return { tone: 'warn', title: r.title, body: r.error };
  if (r.label === 'attack') {
    if (r.harm_caused) return { tone: 'bad', title: `Harm caused: ${r.title}`, body: `Real effects: ${effects || 'none'}${denials ? `. Flagged by ${denials}.` : '.'}` };
    if (denials) return { tone: 'good', title: `Stopped: ${r.title}`, body: `Blocked by ${denials}. No real effect happened.` };
    return { tone: 'good', title: `No harm: ${r.title}`, body: r.verdict };
  }
  if (!r.passed) return { tone: 'warn', title: `Normal work affected: ${r.title}`, body: r.verdict };
  return { tone: 'info', title: `Done: ${r.title}`, body: effects ? `Effects: ${effects}` : r.verdict };
}

/** The attacks shown first: one of each main kind. */
const FEATURED = [
  'borrowed-refund',
  'refund-with-operator-key',
  'multi-step-injection',
  'data-exfiltration',
  'forged-chain-at-entry',
  'locked-agent-unlocks-itself',
];

/** Demo-only controls: a loop of normal work, and one button per attack. */
export function DemoPanel() {
  const demo = useStore((s) => s.demo);
  const [scenarios, setScenarios] = useState<Scenario[]>([]);
  const [traffic, setTraffic] = useState(false);
  const [running, setRunning] = useState<string | null>(null);
  const [open, setOpen] = useState(true);
  const [hint, setHint] = useState<Scenario | null>(null);
  const [showAll, setShowAll] = useState(false);

  useEffect(() => {
    if (!demo) return;
    void act('Loading demo scenarios', async (a) => {
      const [list, t] = await Promise.all([a.scenarios(), a.traffic()]);
      setScenarios(list);
      setTraffic(t.on);
    });
  }, [demo]);

  if (!demo) return null;
  const all = scenarios.filter((s) => s.label === 'attack');
  // A short list first; the whole catalogue is one click away.
  const featured = FEATURED.map((id) => all.find((s) => s.id === id)).filter((s): s is Scenario => !!s);
  const attacks = showAll || featured.length === 0 ? all : featured;

  const toggleTraffic = async () => {
    const t = await act('Changing demo traffic', (a) => a.setTraffic(!traffic));
    if (t) setTraffic(t.on);
  };
  const run = async (s: Scenario) => {
    setRunning(s.id);
    const r = await act(`Running “${s.title}”`, (a) => a.runScenario(s.id));
    setRunning(null);
    if (!r) return;
    const d = describe(r);
    store.toast(d.tone, d.title, d.body, undefined, 9000);
  };

  return (
    <section className="hud demo" aria-labelledby="demo-title" data-tour="demo">
      <div className="demo-head">
        <h2 id="demo-title">Demo traffic</h2>
        <button className="link" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? 'Hide' : 'Show'}
        </button>
      </div>
      {open && (
        <>
          <label className="switch">
            <input type="checkbox" checked={traffic} onChange={toggleTraffic} />
            <span className="track" aria-hidden="true" />
            Normal work {traffic ? 'running' : 'off'}
          </label>
          <p className="small muted">Launch an attack through the real gateway:</p>
          <ul className="attacks">
            {attacks.map((s) => (
              <li key={s.id}>
                <button
                  className="attack-btn"
                  title={s.title}
                  disabled={!!running}
                  aria-describedby="demo-story"
                  onClick={() => run(s)}
                  onMouseEnter={() => setHint(s)}
                  onFocus={() => setHint(s)}
                >
                  <span aria-hidden="true">{running === s.id ? '⏳' : '🚀'}</span> {s.title}
                </button>
              </li>
            ))}
          </ul>
          {featured.length > 0 && all.length > featured.length && (
            <button className="link small" aria-expanded={showAll} onClick={() => setShowAll(!showAll)}>
              {showAll ? 'Show the main attacks only' : `Show all ${all.length} attacks`}
            </button>
          )}
          <p id="demo-story" className="story small">
            {hint ? hint.story : 'Point at an attack to read what it does.'}
          </p>
        </>
      )}
    </section>
  );
}
