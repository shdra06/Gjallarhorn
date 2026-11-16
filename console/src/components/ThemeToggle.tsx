import { store, useStore } from '../state/store';
import type { ThemePref } from '../state/theme';
import { IconMonitor, IconMoon, IconSun } from './Icons';

const options: { id: ThemePref; label: string; icon: () => React.JSX.Element }[] = [
  { id: 'light', label: 'Light theme', icon: () => <IconSun size={14} /> },
  { id: 'dark', label: 'Dark theme', icon: () => <IconMoon size={14} /> },
  { id: 'system', label: 'Follow the system theme', icon: () => <IconMonitor size={14} /> },
];

/** Light, dark, or follow the system. Remembered in this browser. */
export function ThemeToggle() {
  const pref = useStore((s) => s.themePref);
  return (
    <div className="theme-toggle" role="group" aria-label="Theme">
      {options.map((o) => (
        <button key={o.id} className={pref === o.id ? 'on' : ''} aria-pressed={pref === o.id} aria-label={o.label} title={o.label} onClick={() => store.setTheme(o.id)}>
          {o.icon()}
        </button>
      ))}
    </div>
  );
}
