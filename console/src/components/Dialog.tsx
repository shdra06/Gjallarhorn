import { useEffect, useRef, useState, type ReactNode } from 'react';

export interface ConfirmOptions {
  title: string;
  body: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  /** Ask for a short text, e.g. the reason for a lockdown. */
  input?: { label: string; initial: string };
  /** Offer a choice, e.g. how long a lockdown lasts. */
  choice?: { label: string; options: { value: string; label: string }[]; initial: string };
}

export interface ConfirmResult {
  text: string;
  choice: string;
}

type Pending = ConfirmOptions & { resolve: (r: ConfirmResult | null) => void };

let show: ((p: Pending) => void) | null = null;

/** Asks the operator to confirm an action. Resolves to null if they cancel. */
export function confirmAction(opts: ConfirmOptions): Promise<ConfirmResult | null> {
  return new Promise((resolve) => {
    if (!show) return resolve(null);
    show({ ...opts, resolve });
  });
}

/** Accessible modal: focus moves in, Tab stays inside, Escape cancels, focus returns after. */
export function Modal({ title, onClose, children, labelledBy = 'modal-title' }: { title: string; onClose: () => void; children: ReactNode; labelledBy?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    const el = ref.current;
    const focusables = () => [...(el?.querySelectorAll<HTMLElement>('button, input, select, textarea, a[href], [tabindex]:not([tabindex="-1"])') ?? [])].filter((x) => !x.hasAttribute('disabled'));
    (el?.querySelector<HTMLElement>('[data-autofocus]') ?? focusables()[0])?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      } else if (e.key === 'Tab') {
        const f = focusables();
        if (!f.length) return;
        const first = f[0]!;
        const last = f[f.length - 1]!;
        if (e.shiftKey && document.activeElement === first) {
          last.focus();
          e.preventDefault();
        } else if (!e.shiftKey && document.activeElement === last) {
          first.focus();
          e.preventDefault();
        }
      }
    };
    el?.addEventListener('keydown', onKey);
    return () => {
      el?.removeEventListener('keydown', onKey);
      before?.focus?.();
    };
  }, [onClose]);
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby={labelledBy} ref={ref}>
        <h2 id={labelledBy}>{title}</h2>
        {children}
      </div>
    </div>
  );
}

export function DialogHost() {
  const [p, setP] = useState<Pending | null>(null);
  const [text, setText] = useState('');
  const [choice, setChoice] = useState('');
  useEffect(() => {
    show = (next) => {
      setText(next.input?.initial ?? '');
      setChoice(next.choice?.initial ?? '');
      setP(next);
    };
    return () => {
      show = null;
    };
  }, []);
  if (!p) return null;
  const close = (ok: boolean) => {
    p.resolve(ok ? { text: text.trim(), choice } : null);
    setP(null);
  };
  return (
    <Modal title={p.title} onClose={() => close(false)} labelledBy="confirm-title">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (p.input && !text.trim()) return;
          close(true);
        }}
      >
        <div className="modal-body">{p.body}</div>
        {p.input && (
          <label className="field">
            <span>{p.input.label}</span>
            <input value={text} onChange={(e) => setText(e.target.value)} maxLength={200} data-autofocus />
          </label>
        )}
        {p.choice && (
          <label className="field">
            <span>{p.choice.label}</span>
            <select value={choice} onChange={(e) => setChoice(e.target.value)}>
              {p.choice.options.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </label>
        )}
        <div className="modal-actions">
          <button type="button" className="btn" onClick={() => close(false)} data-autofocus={p.input ? undefined : true}>
            Cancel
          </button>
          <button type="submit" className={`btn ${p.danger ? 'danger' : 'primary'}`} disabled={!!p.input && !text.trim()}>
            {p.confirmLabel}
          </button>
        </div>
      </form>
    </Modal>
  );
}
