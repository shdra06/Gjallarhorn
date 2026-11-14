import { useEffect, useState } from 'react';
import { kindOf, shortName } from '../model/flights';
import { store } from '../state/store';

/**
 * Tells screen-reader users when Gjallarhorn blocks or flags a call, a few seconds apart and
 * summarised when many happen at once, so the 3D view is never the only way to know.
 */
export function LiveAnnouncer() {
  const [text, setText] = useState('');
  useEffect(() => {
    let pending: string[] = [];
    const off = store.onLive((e) => {
      if (e.type === 'lockdown' && e.note?.principal) pending.push(`${shortName(e.note.principal)} was locked: ${e.note.reason ?? ''}`);
      if (e.type !== 'decision' || !e.facts) return;
      const k = kindOf(e);
      if (k === 'allowed' || store.get().replay.active) return;
      const key = e.decision?.rule_id || e.decision?.code;
      pending.push(`${k === 'blocked' ? 'Blocked' : 'Flagged'}: ${shortName(e.facts.actor.id)} using ${e.facts.target}, by ${key}`);
    });
    const timer = setInterval(() => {
      if (!pending.length) return;
      setText(pending.length === 1 ? pending[0]! : `${pending.length} security events. Latest: ${pending[pending.length - 1]}`);
      pending = [];
    }, 2500);
    return () => {
      off();
      clearInterval(timer);
    };
  }, []);
  return (
    <div className="sr-only" aria-live="polite" aria-atomic="true">
      {text}
    </div>
  );
}
