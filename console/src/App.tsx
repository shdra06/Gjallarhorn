import { useEffect, useState } from 'react';
import { AirspaceView } from './components/AirspaceView';
import { DialogHost } from './components/Dialog';
import { Drawer } from './components/Drawer';
import { HelpDialog } from './components/HelpDialog';
import { LiveAnnouncer } from './components/LiveAnnouncer';
import { Landing } from './components/Landing';
import { PolicyView } from './components/PolicyView';
import { TableView } from './components/TableView';
import { Toasts } from './components/Toasts';
import { TopBar } from './components/TopBar';
import { Tour } from './components/Tour';
import { connect, savedToken } from './state/session';
import { store, useStore, type View } from './state/store';

const views: Record<string, View> = { '1': 'airspace', '2': 'table', '3': 'policy' };

function typing(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null;
  if (!el) return false;
  return el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName);
}

export function App() {
  const conn = useStore((s) => s.conn);
  const view = useStore((s) => s.view);
  const [booting, setBooting] = useState(() => !!savedToken());

  useEffect(() => {
    const t = savedToken();
    if (!t) return;
    connect(t)
      .catch(() => undefined)
      .finally(() => setBooting(false));
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        const s = store.get();
        if (s.helpOpen) store.set({ helpOpen: false });
        else if (s.selected) store.select(null);
        return;
      }
      if (typing(e.target) || e.ctrlKey || e.metaKey || e.altKey) return;
      const v = views[e.key];
      if (v) {
        store.set({ view: v });
        e.preventDefault();
      } else if (e.key === '?' || (e.key === '/' && e.shiftKey)) {
        store.set({ helpOpen: true });
        e.preventDefault();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const signedIn = conn === 'live' || conn === 'connecting' || conn === 'offline';
  if (booting && conn !== 'live') {
    return (
      <div className="boot" role="status">
        Connecting to Gjallarhorn…
      </div>
    );
  }
  if (!signedIn) return <Landing />;

  return (
    <div className={`app view-${view}`}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <TopBar />
      <main id="main" tabIndex={-1}>
        {view === 'airspace' && <AirspaceView />}
        {view === 'table' && <TableView />}
        {view === 'policy' && <PolicyView />}
      </main>
      <Drawer />
      <Toasts />
      <LiveAnnouncer />
      <HelpDialog />
      <DialogHost />
      {view === 'airspace' && <Tour />}
    </div>
  );
}
