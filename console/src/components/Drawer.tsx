import { useEffect, useRef } from 'react';
import { store, useStore } from '../state/store';
import { IncidentPanel } from './IncidentPanel';
import { NodePanel } from './NodePanel';

/** The side panel for the selected incident or node. Esc or × closes it. */
export function Drawer() {
  const selected = useStore((s) => s.selected);
  const ref = useRef<HTMLElement>(null);
  const key = selected ? `${selected.kind}:${selected.kind === 'node' ? selected.id : selected.seq}` : '';

  useEffect(() => {
    if (!key) return;
    const before = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>('h2')?.focus();
    return () => {
      if (before && document.contains(before)) before.focus();
    };
    // Focus moves only when a different item is opened.
  }, [key]);

  if (!selected) return null;
  return (
    <aside className="drawer" ref={ref} aria-label={selected.kind === 'node' ? 'Details' : 'Incident'}>
      <button className="icon-btn drawer-close" aria-label="Close panel" onClick={() => store.select(null)}>
        ×
      </button>
      {selected.kind === 'incident' ? <IncidentPanel key={key} seq={selected.seq} /> : <NodePanel key={key} id={selected.id} />}
    </aside>
  );
}
