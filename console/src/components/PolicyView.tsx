import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError } from '../api/client';
import type { Change, Policy, Rule, SimReport } from '../api/types';
import { useDebounced } from '../hooks';
import { reasonKey, shortName } from '../model/flights';
import { describeMatch, plural } from '../model/format';
import { getApi } from '../state/session';
import { store } from '../state/store';
import { confirmAction } from './Dialog';

interface Check {
  valid: boolean;
  errors: string[];
  report: SimReport | null;
  busy: boolean;
}

function labelDelta(r: SimReport, label: string): number {
  const l = r.labels?.[label];
  return l ? l.blocked_after - l.blocked_before : 0;
}

function ChangeList({ title, changes, tone }: { title: string; changes: Change[]; tone: 'blocked' | 'allowed' }) {
  if (!changes.length) return null;
  return (
    <details className="changes" open={changes.length <= 6}>
      <summary>
        {title} ({changes.length})
      </summary>
      <ul>
        {changes.slice(0, 40).map((c) => (
          <li key={c.seq} className={tone}>
            <button className="link" onClick={() => store.select({ kind: 'incident', seq: c.seq })}>
              #{c.seq}
            </button>{' '}
            {c.facts.chain.map((p) => shortName(p.id)).join(' › ')} → <code>{c.facts.target}</code>{' '}
            <span className="muted">
              {reasonKey(c.before)} → {reasonKey(c.after)}
            </span>
            {c.label && <span className={`label ${c.label}`}>{c.label}</span>}
          </li>
        ))}
      </ul>
    </details>
  );
}

function RuleList({ rules, onPick }: { rules: Rule[]; onPick: (id: string) => void }) {
  return (
    <ul className="rule-list">
      {rules.map((r) => (
        <li key={r.id}>
          <button className="rule-card" onClick={() => onPick(r.id)}>
            <span className="rule-title">
              <code>{r.id}</code>
              {r.lockdown && <span className="badge blocked small">locks</span>}
              {r.mode === 'monitor' && <span className="badge flagged small">monitor</span>}
              {r.effect === 'allow' && <span className="badge allowed small">allow</span>}
            </span>
            {r.description && <span className="small">{r.description}</span>}
            <span className="small muted">{describeMatch(r.match as Record<string, unknown>).join(' · ')}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

export function PolicyView() {
  const [saved, setSaved] = useState('');
  const [text, setText] = useState('');
  const [etag, setEtag] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [check, setCheck] = useState<Check>({ valid: true, errors: [], report: null, busy: false });
  const area = useRef<HTMLTextAreaElement>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const r = await getApi().policy();
      const t = JSON.stringify(r.data, null, 2);
      setSaved(t);
      setText(t);
      setEtag(r.etag);
    } catch (e) {
      store.toast('bad', 'Could not load the policy', (e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => void load(), [load]);

  const parsed = useMemo((): { policy: Policy | null; error: string } => {
    try {
      return { policy: JSON.parse(text) as Policy, error: '' };
    } catch (e) {
      return { policy: null, error: (e as Error).message };
    }
  }, [text]);

  const debounced = useDebounced(text, 700);
  const dirty = text !== saved;

  useEffect(() => {
    if (loading || !dirty) {
      setCheck({ valid: true, errors: [], report: null, busy: false });
      return;
    }
    let p: unknown;
    try {
      p = JSON.parse(debounced);
    } catch {
      return;
    }
    let live = true;
    setCheck((c) => ({ ...c, busy: true }));
    (async () => {
      const api = getApi();
      const v = await api.validatePolicy(p).catch(() => ({ valid: false, errors: ['The control API could not validate this policy.'] }));
      if (!live) return;
      if (!v.valid) {
        setCheck({ valid: false, errors: v.errors ?? [], report: null, busy: false });
        return;
      }
      const report = await api.simulate(p).catch(() => null);
      if (live) setCheck({ valid: true, errors: [], report, busy: false });
    })();
    return () => {
      live = false;
    };
  }, [debounced, dirty, loading]);

  const pick = (id: string) => {
    const el = area.current;
    if (!el) return;
    const i = text.indexOf(`"id": ${JSON.stringify(id)}`);
    if (i < 0) return;
    el.focus();
    el.setSelectionRange(i, i + id.length + 8);
    const line = text.slice(0, i).split('\n').length;
    el.scrollTop = Math.max(0, (line - 3) * 20);
  };

  const save = async () => {
    if (!parsed.policy || !check.valid) return;
    const r = check.report;
    if (r && !r.safe) {
      const benign = labelDelta(r, 'benign');
      const attack = labelDelta(r, 'attack');
      const ok = await confirmAction({
        title: 'Save a policy that failed the replay check?',
        body: (
          <p>
            Replaying recorded traffic, this policy {benign > 0 ? `blocks ${plural(benign, 'more normal call')}` : ''}
            {benign > 0 && attack < 0 ? ' and ' : ''}
            {attack < 0 ? `lets ${plural(-attack, 'attack call')} through that were blocked before` : ''}
            {benign <= 0 && attack >= 0 ? 'does not pass the safety check' : ''}.
          </p>
        ),
        confirmLabel: 'Save anyway',
        danger: true,
      });
      if (!ok) return;
    }
    setSaving(true);
    try {
      const res = await getApi().savePolicy(parsed.policy, etag);
      const t = JSON.stringify(res.data, null, 2);
      setSaved(t);
      setText(t);
      setEtag(res.etag);
      store.toast('good', 'Policy saved', 'It is live on the next call.');
    } catch (e) {
      const err = e as ApiError;
      if (err.status === 412) {
        store.toast('warn', 'Someone changed the policy', 'Reload to see their version, then make your change again.', { label: 'Reload', run: () => void load() });
      } else {
        store.toast('bad', 'The policy was not saved', [err.message, ...(err.details ?? [])].join(': '));
      }
    } finally {
      setSaving(false);
    }
  };

  const r = check.report;
  const lines = text.split('\n').length;

  return (
    <div className="policy-view">
      <h1 className="sr-only">Policy editor</h1>
      <aside className="policy-side" aria-label="Rules">
        <h2>Rules</h2>
        {parsed.policy?.rules?.length ? <RuleList rules={parsed.policy.rules} onPick={pick} /> : <p className="muted small">No rules.</p>}
        <p className="small muted">Deny wins. Calls no rule matches get the default ({parsed.policy?.default_effect || 'allow'}).</p>
      </aside>

      <section className="editor" aria-labelledby="editor-title">
        <div className="editor-bar">
          <h2 id="editor-title">Policy JSON</h2>
          <span className={`dirty small ${dirty ? 'on' : ''}`}>{dirty ? 'Unsaved changes' : 'Saved'}</span>
          <button className="btn small" onClick={() => parsed.policy && setText(JSON.stringify(parsed.policy, null, 2))} disabled={!parsed.policy}>
            Format
          </button>
          <button className="btn small" onClick={() => setText(saved)} disabled={!dirty}>
            Discard
          </button>
          <button className="btn small" onClick={() => void load()}>
            Reload
          </button>
          <button className="btn primary small" onClick={save} disabled={!dirty || !parsed.policy || !check.valid || check.busy || saving}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
        <div className="code">
          <div className="gutter" aria-hidden="true">
            {Array.from({ length: lines }, (_, i) => (
              <span key={i}>{i + 1}</span>
            ))}
          </div>
          <textarea
            ref={area}
            value={text}
            onChange={(e) => setText(e.target.value)}
            spellCheck={false}
            aria-label="Policy JSON"
            aria-invalid={!!parsed.error || !check.valid}
            aria-describedby="policy-status"
            onScroll={(e) => {
              const g = e.currentTarget.previousElementSibling as HTMLElement | null;
              if (g) g.scrollTop = e.currentTarget.scrollTop;
            }}
            onKeyDown={(e) => {
              if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                e.preventDefault();
                void save();
              }
              if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey) {
                // Indent instead of leaving the editor; Esc then Tab moves on.
                const el = e.currentTarget;
                if (el.dataset.escape === '1') return;
                e.preventDefault();
                const s = el.selectionStart;
                setText(text.slice(0, s) + '  ' + text.slice(el.selectionEnd));
                requestAnimationFrame(() => el.setSelectionRange(s + 2, s + 2));
              }
              e.currentTarget.dataset.escape = e.key === 'Escape' ? '1' : '';
            }}
          />
        </div>
        <p className="small muted">Tab indents. Press Esc, then Tab, to leave the editor. Ctrl+S saves.</p>
      </section>

      <section className="simulation" aria-labelledby="sim-title" aria-live="polite" id="policy-status">
        <h2 id="sim-title">Live replay check</h2>
        {!dirty && <p className="muted">Edit the policy and Gjallarhorn replays every recorded call against it before you save.</p>}
        {dirty && parsed.error && (
          <p className="error">
            <strong>Not valid JSON:</strong> {parsed.error}
          </p>
        )}
        {dirty && !parsed.error && check.busy && <p className="muted">Replaying recorded traffic…</p>}
        {dirty && !parsed.error && !check.valid && (
          <div className="error">
            <strong>The policy is invalid:</strong>
            <ul>
              {check.errors.map((e) => (
                <li key={e}>{e}</li>
              ))}
            </ul>
          </div>
        )}
        {dirty && !parsed.error && check.valid && r && (
          <>
            <div className={`safe ${r.safe ? 'yes' : 'no'}`}>
              <span aria-hidden="true">{r.safe ? '✓' : '✕'}</span>
              {r.safe ? 'Safe: it still blocks every attack it blocked before, and no more normal work.' : 'Not safe: it lets a blocked attack through, or blocks more normal work.'}
            </div>
            <dl className="sim-stats">
              <div>
                <dt>Calls replayed</dt>
                <dd>{r.evaluated}</dd>
              </div>
              <div className="blocked">
                <dt>Newly blocked</dt>
                <dd>{r.newly_blocked?.length ?? 0}</dd>
              </div>
              <div className="allowed">
                <dt>Newly allowed</dt>
                <dd>{r.newly_allowed?.length ?? 0}</dd>
              </div>
            </dl>
            {r.labels && (
              <table className="mini">
                <caption className="sr-only">Blocked calls by label, before and after</caption>
                <thead>
                  <tr>
                    <th scope="col">Traffic</th>
                    <th scope="col">Calls</th>
                    <th scope="col">Blocked now</th>
                    <th scope="col">With this policy</th>
                  </tr>
                </thead>
                <tbody>
                  {Object.entries(r.labels).map(([k, v]) => (
                    <tr key={k} className={k === 'benign' && v.blocked_after > v.blocked_before ? 'bad' : k === 'attack' && v.blocked_after < v.blocked_before ? 'bad' : ''}>
                      <th scope="row">{k === 'benign' ? 'Normal work' : k === 'attack' ? 'Attacks' : k}</th>
                      <td>{v.total}</td>
                      <td>{v.blocked_before}</td>
                      <td>{v.blocked_after}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <ChangeList title="Newly blocked" changes={r.newly_blocked ?? []} tone="blocked" />
            <ChangeList title="Newly allowed" changes={r.newly_allowed ?? []} tone="allowed" />
            {r.truncated && <p className="small muted">More calls changed than are listed here.</p>}
            <p className="small muted">The check can only judge traffic Gjallarhorn has recorded. A rule for an attack it has never seen will look safe here.</p>
          </>
        )}
      </section>
    </div>
  );
}
