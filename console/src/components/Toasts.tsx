import { store, useStore } from '../state/store';

const icon = { info: 'ℹ', good: '✓', bad: '✕', warn: '⚠' } as const;

export function Toasts() {
  const toasts = useStore((s) => s.toasts);
  return (
    <section className="toasts" aria-label="Notifications" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={`toast ${t.tone}`} role={t.tone === 'bad' ? 'alert' : 'status'}>
          <span className="toast-icon" aria-hidden="true">
            {icon[t.tone]}
          </span>
          <div className="toast-text">
            <strong>{t.title}</strong>
            {t.body && <p>{t.body}</p>}
            {t.action && (
              <button
                className="link"
                onClick={() => {
                  t.action!.run();
                  store.dismiss(t.id);
                }}
              >
                {t.action.label}
              </button>
            )}
          </div>
          <button className="icon-btn" aria-label="Dismiss notification" onClick={() => store.dismiss(t.id)}>
            ×
          </button>
        </div>
      ))}
    </section>
  );
}
