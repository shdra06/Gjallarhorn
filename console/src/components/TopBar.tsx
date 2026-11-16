import { act, signOut } from '../state/session';
import { store, useStore, type View } from '../state/store';
import { confirmAction } from './Dialog';
import { Logo } from './Logo';
import { ThemeToggle } from './ThemeToggle';

const tabs: { id: View; label: string; key: string }[] = [
  { id: 'airspace', label: 'Airspace', key: '1' },
  { id: 'table', label: 'Table', key: '2' },
  { id: 'policy', label: 'Policy', key: '3' },
];

const connText = { idle: 'Offline', connecting: 'Connecting…', live: 'Live', offline: 'Reconnecting…', unauthorized: 'Signed out' } as const;

export function ModeSwitch() {
  const enforcing = useStore((s) => s.enforcing);
  const set = async (on: boolean) => {
    if (on === enforcing) return;
    if (!on) {
      const ok = await confirmAction({
        title: 'Switch to monitor-only?',
        body: (
          <p>
            Gjallarhorn will keep recording every decision but <strong>will stop nothing</strong>. Attacks will get through. Use this for a first rollout or to measure what a rule
            would do.
          </p>
        ),
        confirmLabel: 'Switch to monitor-only',
        danger: true,
      });
      if (!ok) return;
    }
    const res = await act('Changing the mode', (a) => a.setMode(on));
    if (res !== undefined) {
      store.set({ enforcing: res });
      store.toast(res ? 'good' : 'warn', res ? 'Enforcing: denied calls are blocked' : 'Monitor-only: calls are recorded, not blocked');
    }
  };
  return (
    <div className="mode-switch" role="group" aria-label="Gjallarhorn mode" data-tour="mode">
      <button className={enforcing ? 'on enforce' : ''} aria-pressed={enforcing === true} onClick={() => set(true)}>
        <span aria-hidden="true">🛡</span> Enforcing
      </button>
      <button className={enforcing === false ? 'on monitor' : ''} aria-pressed={enforcing === false} onClick={() => set(false)}>
        <span aria-hidden="true">👁</span> Monitor-only
      </button>
    </div>
  );
}

export function TopBar() {
  const view = useStore((s) => s.view);
  const conn = useStore((s) => s.conn);
  const reduced = useStore((s) => s.reducedMotion);
  return (
    <header className="topbar">
      <div className="brand">
        <Logo />
        <span>Gjallarhorn</span>
      </div>
      <nav aria-label="Views" className="tabs" data-tour="views">
        {tabs.map((t) => (
          <button key={t.id} className={view === t.id ? 'active' : ''} aria-current={view === t.id ? 'page' : undefined} onClick={() => store.set({ view: t.id })}>
            {t.label}
            <kbd aria-hidden="true">{t.key}</kbd>
          </button>
        ))}
      </nav>
      <div className="topbar-right">
        <ModeSwitch />
        <span className={`conn ${conn}`} role="status" aria-label={`Connection: ${connText[conn]}`}>
          <span className="dot" aria-hidden="true" />
          {connText[conn]}
        </span>
        <ThemeToggle />
        <button className="icon-btn" aria-pressed={reduced} title="Reduce motion" aria-label="Reduce motion" onClick={() => store.setReducedMotion(!reduced)}>
          {reduced ? '◼' : '◆'}
        </button>
        <button className="icon-btn" title="Help and keyboard shortcuts (?)" aria-label="Help and keyboard shortcuts" onClick={() => store.set({ helpOpen: true })}>
          ?
        </button>
        <button className="btn ghost small" onClick={signOut}>
          Sign out
        </button>
      </div>
    </header>
  );
}
