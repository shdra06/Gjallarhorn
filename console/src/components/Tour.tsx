import { useEffect, useLayoutEffect, useState, type CSSProperties } from 'react';
import { useStore } from '../state/store';

const KEY = 'gj.toured';

interface TourStep {
  target?: string;
  title: string;
  body: string;
  demoOnly?: boolean;
}

const steps: TourStep[] = [
  {
    title: 'This is your airspace',
    body: 'Every glowing line is one call: a person, an app or an agent asking an agent, a tool or a model. Work flows left to right.',
  },
  {
    target: 'demo',
    demoOnly: true,
    title: 'Start some traffic',
    body: 'Turn on normal work, then launch an attack. Try “Refund borrowed through another agent”, and watch the last leg get cut off in red.',
  },
  {
    target: 'feed',
    title: 'Click what was stopped',
    body: 'Every call Gjallarhorn stops shows up here, and as a red ✕ in the sky. Open one to see the whole chain, ask the AI co-pilot what happened, or lock the agent.',
  },
  {
    target: 'mode',
    title: 'Monitor or enforce',
    body: 'Monitor-only records everything but stops nothing, which is what you have without Gjallarhorn. Enforcing blocks.',
  },
  {
    target: 'views',
    title: 'Same data, other views',
    body: 'Prefer rows to 3D? The table shows every call. The policy view replays real traffic against your rule before you save it.',
  },
];

let restart: (() => void) | null = null;

export function restartTour(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // ignore
  }
  restart?.();
}

/** A short first-run guide to the moment that matters: an attack, stopped, explained. */
export function Tour() {
  const demo = useStore((s) => s.demo);
  const [active, setActive] = useState(() => {
    try {
      return localStorage.getItem(KEY) !== '1';
    } catch {
      return false;
    }
  });
  const [i, setI] = useState(0);
  const [rect, setRect] = useState<DOMRect | null>(null);
  const list = steps.filter((s) => !s.demoOnly || demo);
  const step = list[Math.min(i, list.length - 1)]!;

  useEffect(() => {
    restart = () => {
      setI(0);
      setActive(true);
    };
    return () => {
      restart = null;
    };
  }, []);

  useLayoutEffect(() => {
    if (!active || !step.target) {
      setRect(null);
      return;
    }
    const measure = () => {
      const el = document.querySelector(`[data-tour="${step.target}"]`);
      setRect(el ? el.getBoundingClientRect() : null);
    };
    measure();
    window.addEventListener('resize', measure);
    const t = setInterval(measure, 500);
    return () => {
      window.removeEventListener('resize', measure);
      clearInterval(t);
    };
  }, [active, step.target]);

  if (!active) return null;
  const done = () => {
    try {
      localStorage.setItem(KEY, '1');
    } catch {
      // ignore
    }
    setActive(false);
  };
  const last = i >= list.length - 1;

  const pad = 8;
  const card: CSSProperties = rect
    ? {
        top: Math.min(window.innerHeight - 220, Math.max(70, rect.top)),
        left: rect.left > window.innerWidth / 2 ? Math.max(16, rect.left - 360) : Math.min(window.innerWidth - 356, rect.right + 16),
      }
    : { top: '50%', left: '50%', transform: 'translate(-50%, -50%)' };

  return (
    <div className="tour" role="dialog" aria-modal="false" aria-labelledby="tour-title" aria-describedby="tour-body">
      {rect && (
        <div
          className="tour-spot"
          style={{ top: rect.top - pad, left: rect.left - pad, width: rect.width + pad * 2, height: rect.height + pad * 2 }}
          aria-hidden="true"
        />
      )}
      <div className="tour-card" style={card}>
        <p className="eyebrow">
          Step {Math.min(i, list.length - 1) + 1} of {list.length}
        </p>
        <h2 id="tour-title">{step.title}</h2>
        <p id="tour-body">{step.body}</p>
        <div className="tour-actions">
          <button className="link" onClick={done}>
            Skip the tour
          </button>
          {i > 0 && (
            <button className="btn small" onClick={() => setI(i - 1)}>
              Back
            </button>
          )}
          <button className="btn primary small" onClick={() => (last ? done() : setI(i + 1))} autoFocus>
            {last ? 'Done' : 'Next'}
          </button>
        </div>
      </div>
    </div>
  );
}
