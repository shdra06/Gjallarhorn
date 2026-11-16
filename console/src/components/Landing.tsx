import { useEffect, useRef, useState } from 'react';
import { startDemo } from '../state/session';
import { useStore } from '../state/store';
import type { Hero } from '../three/Hero';
import { webglAvailable } from '../three/webgl';
import { IconArrowRight, IconGithub, IconKey } from './Icons';
import { LoginForm } from './Login';
import { Logo } from './Logo';
import { ThemeToggle } from './ThemeToggle';

const REPO = 'https://github.com/shdra06/Gjallarhorn';
const DOC = (f: string) => `${REPO}/blob/main/${f}`;

/** The live 3D airspace behind the hero, loaded on demand. Shows where the attack is cut off. */
function HeroScene() {
  const host = useRef<HTMLDivElement>(null);
  const theme = useStore((s) => s.theme);
  const reduced = useStore((s) => s.reducedMotion);
  const [cut, setCut] = useState<{ x: number; y: number; n: number } | null>(null);

  useEffect(() => {
    if (!host.current || !webglAvailable()) return;
    let hero: Hero | null = null;
    let gone = false;
    import('../three/Hero')
      .then(({ Hero }) => {
        if (gone || !host.current) return;
        hero = new Hero(host.current, {
          reducedMotion: reduced,
          theme,
          onCut: (x, y) => setCut((c) => ({ x, y, n: (c?.n ?? 0) + 1 })),
        });
      })
      .catch(() => undefined);
    return () => {
      gone = true;
      hero?.dispose();
    };
  }, [theme, reduced]);

  return (
    <div className="hero-scene" aria-hidden="true">
      <div className="hero-canvas" ref={host} />
      {cut && (
        <div key={cut.n} className="hero-cut" style={{ left: `${cut.x * 100}%`, top: `${cut.y * 100}%` }}>
          ✕ refunds-need-an-operator
        </div>
      )}
    </div>
  );
}

function Label({ children }: { children: string }) {
  return <p className="l-label">[ {children} ]</p>;
}

function Copy({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="l-copy"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? 'Copied' : 'Copy'}
    </button>
  );
}

const SNIPPETS: Record<string, { file: string; code: string }> = {
  'Run the demo': {
    file: 'terminal',
    code: `git clone https://github.com/shdra06/Gjallarhorn.git
cd Gjallarhorn
./demo.sh          # builds once (10-15 min), then starts in seconds
# open http://localhost:18788/ and click "Start the demo"`,
  },
  'Add the plugin': {
    file: 'bifrost config.json',
    code: `"plugins": [{
  "enabled": true,
  "name": "gjallarhorn",
  "path": "/app/plugins/gjallarhorn.so",
  "config": {
    "principals": [ ... ],
    "tickets": { "active_key": "k1", "keys": { "k1": "env:GJ_TICKET_KEY" } },
    "recorder": { "path": "/data/gjallarhorn-flight.jsonl" },
    "control": { "listen": "127.0.0.1:8787", "admin_token": "env:GJ_ADMIN_TOKEN" },
    "policy": { "version": 1, "rules": [ ... ] }
  }
}]`,
  },
  'Write a rule': {
    file: 'policy rule',
    code: `{
  "id": "refunds-need-an-operator",
  "description": "Refunds only when a human operator started the chain",
  "effect": "deny",
  "match":  { "planes": ["mcp"], "targets": ["billing.refund*"] },
  "unless": { "origin_kinds": ["human"], "origin_labels": { "role": "operator" } }
}`,
  },
};

const FEATURES = [
  ['Sees the whole chain', 'Every call carries a signed ticket naming who started the work and every agent it passed through, across models, tools and other agents.'],
  ['Rules about the chain', 'Write "refunds only if a human operator started it". Deny always wins, and a rule can lock an agent the moment it tries.'],
  ['Lock an agent in one click', 'A misbehaving agent is cut off from models, tools and other agents on its next call, about 2 ms later, without locking out its users.'],
  ['A co-pilot that proves its fixes', 'The AI co-pilot explains incidents and suggests rules. Each one is replayed against recorded traffic, and rules that block normal work are rejected.'],
  ['A flight recorder you can trust', 'Every decision is written to a hash-chained log. Replay any minute in 3D, or rebuild an incident step by step.'],
  ['Safe to roll out', 'Start in monitor-only to see what would be blocked. Runs on a standard Bifrost through the official plugin hooks, and fails closed if it stops.'],
];

const FAQ = [
  [
    'Where do I get the access token?',
    'On the demo you don\'t need one: "Start the demo" fetches it for you, because the demo server turns on control.public_demo. Anywhere else the setting is off, and the admin token is the one printed by ./demo.sh ("Admin token: …") or set in your own config.',
  ],
  ['Is the traffic real?', 'The calls are real: real agents call real MCP tools and a model through a real Bifrost gateway on your machine. The company is made up: refunds and emails are written to a ledger and never happen.'],
  ['Is the co-pilot a real LLM?', 'In the demo it is a scripted stand-in model, so the demo runs offline, and it says so on screen. Point it at any model Bifrost serves to use a real one.'],
  ['Does it need the internet?', 'No. Once the images are built, the whole demo runs offline.'],
  ['What doesn\'t it protect against?', 'It decides on who is calling and through which chain. It does not read prompts for hidden instructions, and it trusts whoever holds the admin token. The threat model lists every limit.'],
] as const;

/** What visitors and judges see first: what Gjallarhorn is, and one button into the live demo. */
export function Landing() {
  const conn = useStore((s) => s.conn);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [signIn, setSignIn] = useState<null | 'asked' | 'needed'>(conn === 'unauthorized' ? 'needed' : null);
  const [tab, setTab] = useState('Run the demo');
  const bars = useRef<HTMLDivElement>(null);
  const [barsIn, setBarsIn] = useState(false);

  useEffect(() => {
    if (!signIn) return;
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setSignIn(null);
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [signIn]);

  // The result bars grow when they scroll into view.
  useEffect(() => {
    const el = bars.current;
    if (!el || typeof IntersectionObserver === 'undefined') {
      setBarsIn(true);
      return;
    }
    const io = new IntersectionObserver(([e]) => e?.isIntersecting && setBarsIn(true), { threshold: 0.3 });
    io.observe(el);
    return () => io.disconnect();
  }, []);

  const start = async () => {
    setBusy(true);
    setError('');
    try {
      if (!(await startDemo())) setSignIn('needed');
    } catch {
      setError('Cannot reach the Gjallarhorn control API. Is the demo running? Start it with ./demo.sh');
    } finally {
      setBusy(false);
    }
  };

  const startButton = (
    <button className="l-btn primary" onClick={start} disabled={busy}>
      {busy ? 'Starting…' : 'Start the demo'} <IconArrowRight size={16} />
    </button>
  );

  return (
    <div className="landing">
      <header className="l-nav">
        <a className="l-brand" href="#top">
          <Logo size={28} />
          <span>Gjallarhorn</span>
        </a>
        <nav className="l-links" aria-label="Sections">
          <a href="#results">Results</a>
          <a href="#features">Features</a>
          <a href="#start">Quick start</a>
          <a href="#faq">FAQ</a>
        </nav>
        <div className="l-nav-right">
          <ThemeToggle />
          <a className="l-btn ghost small" href={REPO} target="_blank" rel="noreferrer">
            <IconGithub size={15} /> GitHub
          </a>
          <button className="l-btn small" onClick={() => setSignIn('asked')}>
            Sign in
          </button>
        </div>
      </header>

      <main id="top">
        <section className="l-hero">
          <HeroScene />
          <div className="l-hero-copy">
            <p className="l-badge">
              <span className="l-pulse" aria-hidden="true" /> Open source · a plugin for the Bifrost AI gateway
            </p>
            <h1>
              Know which agent did it.
              <br />
              <span className="l-accent">Stop it mid-air.</span>
            </h1>
            <p className="l-lede">
              Your AI agents call models, tools and each other. When one is tricked, Gjallarhorn sees the whole chain of who asked whom, and blocks the borrowed permission inside the
              gateway, before the money moves.
            </p>
            <div className="l-ctas">
              {startButton}
              <button className="l-btn" onClick={() => setSignIn('asked')}>
                <IconKey size={15} /> Sign in with a token
              </button>
            </div>
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            <div className="l-term" aria-label="Start it yourself">
              <span className="l-term-label">Start in one command</span>
              <code>
                <span className="l-prompt">$</span> ./demo.sh
              </code>
            </div>
          </div>
          <p className="l-hero-caption">
            Live: normal calls in blue. Every few seconds an outside chat tricks support into asking billing for a refund, and the last leg is cut off in red.
          </p>
        </section>

        <section className="l-section" id="results" aria-labelledby="results-title">
          <Label>RESULTS</Label>
          <h2 id="results-title">Our numbers at a glance</h2>
          <div className="l-stats">
            <div>
              <strong>31</strong>
              <span>attacks in the lab, through the real gateway</span>
            </div>
            <div>
              <strong>0</strong>
              <span>caused harm with Gjallarhorn on (12 did without it)</span>
            </div>
            <div>
              <strong>11/11</strong>
              <span>pieces of normal work still pass, no false blocks</span>
            </div>
            <div>
              <strong>≈2 ms</strong>
              <span>from “lock this agent” to its next call refused</span>
            </div>
          </div>

          <div className={`l-bench${barsIn ? ' in' : ''}`} ref={bars}>
            <div className="l-bench-head">
              <Label>ATTACK LAB</Label>
              <p>The same 31 attacks and 11 pieces of normal work, run with Gjallarhorn off and on.</p>
            </div>
            <div className="l-bench-rows">
              <div className="l-bench-row">
                <span className="l-bench-name">Attacks that caused harm</span>
                <div className="l-bars">
                  <div className="l-bar">
                    <span className="l-bar-tag">Without</span>
                    <span className="l-bar-fill bad" style={{ ['--w' as string]: `${(12 / 31) * 100}%` }} />
                    <b>12</b>
                  </div>
                  <div className="l-bar">
                    <span className="l-bar-tag">With</span>
                    <span className="l-bar-fill good" style={{ ['--w' as string]: '0.6%' }} />
                    <b>0</b>
                  </div>
                </div>
              </div>
              <div className="l-bench-row">
                <span className="l-bench-name">Normal work that still passes</span>
                <div className="l-bars">
                  <div className="l-bar">
                    <span className="l-bar-tag">Without</span>
                    <span className="l-bar-fill neutral" style={{ ['--w' as string]: '100%' }} />
                    <b>11</b>
                  </div>
                  <div className="l-bar">
                    <span className="l-bar-tag">With</span>
                    <span className="l-bar-fill good" style={{ ['--w' as string]: '100%' }} />
                    <b>11</b>
                  </div>
                </div>
              </div>
            </div>
            <p className="l-fine">
              Every number links to a raw result file in <a href={DOC('docs/results.md')}>docs/results.md</a>. Run them yourself with <code>./demo.sh lab</code>.
            </p>
          </div>
        </section>

        <section className="l-section" id="features" aria-labelledby="features-title">
          <Label>FEATURES</Label>
          <h2 id="features-title">Built for agents that ask each other for help</h2>
          <div className="l-features">
            {FEATURES.map(([title, body], i) => (
              <article key={title} className="l-feature">
                <span className="l-num">{String(i + 1).padStart(2, '0')}</span>
                <h3>{title}</h3>
                <p>{body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="l-section" id="start" aria-labelledby="start-title">
          <Label>QUICK START</Label>
          <h2 id="start-title">From clone to a stopped attack in one command</h2>
          <div className="l-code">
            <div className="l-tabs" role="tablist" aria-label="Snippets">
              {Object.keys(SNIPPETS).map((k) => (
                <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>
                  {k}
                </button>
              ))}
              <span className="l-file">{SNIPPETS[tab]!.file}</span>
              <Copy text={SNIPPETS[tab]!.code} />
            </div>
            <pre>
              {SNIPPETS[tab]!.code.split('\n').map((line, i) => (
                <span key={i} className="l-line">
                  <span className="l-ln" aria-hidden="true">
                    {i + 1}
                  </span>
                  {line}
                  {'\n'}
                </span>
              ))}
            </pre>
          </div>
        </section>

        <section className="l-section" id="faq" aria-labelledby="faq-title">
          <Label>FAQ</Label>
          <h2 id="faq-title">Questions judges ask</h2>
          <div className="l-faq">
            {FAQ.map(([q, a], i) => (
              <details key={q} open={i === 0}>
                <summary>{q}</summary>
                <p>{a}</p>
              </details>
            ))}
          </div>
          <p className="l-fine">
            More in the <a href={DOC('docs/threat-model.md')}>threat model</a> and the <a href={DOC('README.md')}>README</a>.
          </p>
        </section>

        <section className="l-final">
          <h2>See an attack stopped in under a minute.</h2>
          <p>No sign-up and no token to type. The demo runs entirely on your machine.</p>
          {startButton}
        </section>
      </main>

      <footer className="l-foot">
        <span>Gjallarhorn · Apache-2.0 · a plugin for Bifrost</span>
        <a href={REPO} target="_blank" rel="noreferrer">
          github.com/shdra06/Gjallarhorn
        </a>
      </footer>

      {signIn && (
        <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && setSignIn(null)}>
          <div className="l-signin" role="dialog" aria-modal="true" aria-labelledby="login-title">
            <LoginForm
              onCancel={() => setSignIn(null)}
              note={
                signIn === 'needed' ? (
                  <p className="muted">
                    This server doesn't share its token. Use the one printed in the terminal where <code>./demo.sh</code> started (<em>Admin token: …</em>).
                  </p>
                ) : undefined
              }
            />
          </div>
        </div>
      )}
    </div>
  );
}
