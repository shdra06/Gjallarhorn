import { useEffect, useState } from 'react';
import type { Explanation, Incident, Suggestion, Suggestions } from '../api/types';
import { describeMatch, plural } from '../model/format';
import { shortName } from '../model/flights';
import { act } from '../state/session';
import { isLocked, store, useStore } from '../state/store';
import { DecisionBadge, lockPrincipal, PlaneTag, Route, unlockPrincipal } from './common';
import { confirmAction } from './Dialog';

const statusText = { proven: 'Proven safe', review: 'Needs review', rejected: 'Rejected' } as const;
const statusIcon = { proven: '✓', review: '⚠', rejected: '✕' } as const;

function SuggestionCard({ s, onApply, applying, applied, stale }: { s: Suggestion; onApply: (s: Suggestion) => void; applying: boolean; applied: boolean; stale: boolean }) {
  const match = describeMatch(s.rule.match as Record<string, unknown>);
  const unless = describeMatch(s.rule.unless as Record<string, unknown> | undefined);
  const benign = s.labels?.benign;
  return (
    <li className={`suggestion ${s.status}`}>
      <div className="suggestion-head">
        <span className={`status ${s.status}`}>
          <span aria-hidden="true">{statusIcon[s.status]}</span> {statusText[s.status]}
        </span>
        <span className="source">{s.source === 'llm' ? 'from the model' : s.source === 'synthesized' ? 'from the incident' : s.source}</span>
      </div>
      <code className="rule-id">{s.rule.id || '(unnamed rule)'}</code>
      {s.rule.description && <p className="small">{s.rule.description}</p>}
      {match.length > 0 && (
        <p className="conditions small">
          <span className="muted">Deny when</span> {match.join(' · ')}
          {unless.length > 0 && (
            <>
              {' '}
              <span className="muted">unless</span> {unless.join(' · ')}
            </>
          )}
        </p>
      )}
      <p className={`verdict small ${s.status}`}>{s.verdict}</p>
      {benign && benign.total > 0 && (
        <p className="small muted">
          Normal work replayed: {benign.total} calls, blocked after: {benign.blocked_after}
        </p>
      )}
      {s.collateral_calls && s.collateral_calls.length > 0 && (
        <details className="small">
          <summary>Other calls it would block ({s.collateral})</summary>
          <ul>
            {s.collateral_calls.map((c) => (
              <li key={c}>
                <code>{c}</code>
              </li>
            ))}
          </ul>
        </details>
      )}
      {s.status !== 'rejected' &&
        (stale && !applied ? (
          <p className="small muted">The policy changed. Suggest again to prove this against the new policy.</p>
        ) : (
          <button className={`btn small ${s.status === 'proven' ? 'primary' : ''}`} disabled={applying || applied} onClick={() => onApply(s)}>
            {applied ? 'Applied ✓' : applying ? 'Proving again…' : s.status === 'proven' ? 'Apply rule' : 'Review and apply…'}
          </button>
        ))}
    </li>
  );
}

export function IncidentPanel({ seq }: { seq: number }) {
  const lockdowns = useStore((s) => s.lockdowns);
  const copilot = useStore((s) => s.copilot);
  const [incident, setIncident] = useState<Incident | null>(null);
  const [error, setError] = useState('');
  const [explanation, setExplanation] = useState<Explanation | null>(null);
  const [explaining, setExplaining] = useState(false);
  const [suggestions, setSuggestions] = useState<Suggestions | null>(null);
  const [suggesting, setSuggesting] = useState(false);
  const [applying, setApplying] = useState<string | null>(null);
  const [applied, setApplied] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    act('Loading the incident', (a) => a.incident(seq)).then((i) => {
      if (!live) return;
      if (i) setIncident(i);
      else setError('This call is not in the flight recorder.');
    });
    setExplaining(true);
    act('Asking the co-pilot', (a) => a.explain(seq)).then((x) => {
      if (!live) return;
      setExplanation(x ?? null);
      setExplaining(false);
    });
    return () => {
      live = false;
    };
  }, [seq]);

  const focus = incident?.steps.find((s) => s.focus);
  const actor = focus?.actor ?? '';
  const canLock = actor.startsWith('agent:') || actor.startsWith('service:');

  const suggest = async () => {
    setSuggesting(true);
    setApplied(null);
    const res = await act('Asking for rules', (a) => a.suggest(seq));
    setSuggesting(false);
    if (res) setSuggestions(res);
  };

  const apply = async (s: Suggestion) => {
    if (!suggestions) return;
    const review = s.status === 'review';
    const ok = await confirmAction({
      title: `Apply ${s.rule.id}?`,
      body: (
        <>
          <p>Gjallarhorn proves the rule again against everything recorded up to now, then adds it to the live policy. If someone changed the policy meanwhile, nothing is changed.</p>
          {review && (
            <p className="warn-text">
              This rule would also block {plural(s.collateral, 'other call')} that is not labelled as normal work or attack. Check them before applying.
            </p>
          )}
        </>
      ),
      confirmLabel: review ? 'Apply anyway' : 'Apply rule',
      danger: review,
    });
    if (!ok) return;
    setApplying(s.id);
    const res = await act('Applying the rule', (a) => a.apply(seq, s.rule, suggestions.policy_etag, review));
    setApplying(null);
    if (!res) return;
    if (res.data.applied) {
      setApplied(s.id);
      store.toast('good', `Rule ${res.data.suggestion.rule.id} is live`, 'It was proven again against all recorded traffic before it was applied.');
    } else {
      store.toast('bad', 'Gjallarhorn refused the rule', res.data.suggestion.verdict);
    }
  };

  return (
    <div className="panel incident">
      <p className="eyebrow">Incident · call #{seq}</p>
      <h2 tabIndex={-1}>{explanation?.title ?? (focus ? `${shortName(focus.actor)} → ${focus.target}` : 'Loading…')}</h2>
      {error && <p className="error">{error}</p>}

      {incident && focus && (
        <>
          <div className="incident-meta">
            <DecisionBadge d={focus.decision} monitored={focus.monitored} />
            <PlaneTag plane={focus.plane} />
            <span className="small muted">
              {plural(incident.steps.length, 'call')} in this chain · {incident.blocked} stopped · {incident.executed} ran
            </span>
          </div>
          <Route chain={focus.chain} target={focus.target} stopped={!focus.executed} />
          {canLock &&
            (isLocked(lockdowns, actor) ? (
              <button className="btn" onClick={() => unlockPrincipal(actor)}>
                Unlock {shortName(actor)}
              </button>
            ) : (
              <button className="btn danger" onClick={() => lockPrincipal(actor)}>
                <span aria-hidden="true">⏻</span> Lock {shortName(actor)}
              </button>
            ))}

          <h3>What happened</h3>
          <ol className="steps">
            {incident.steps.map((st) => (
              <li key={st.seq} className={`${st.focus ? 'focus' : ''}`} style={{ marginLeft: `${Math.min(st.depth, 4) * 14}px` }}>
                <span className="step-route">
                  <PlaneTag plane={st.plane} /> <strong>{shortName(st.actor)}</strong> → {st.target}
                </span>
                <DecisionBadge d={st.decision} monitored={st.monitored} />
                {st.focus && <span className="sr-only">(this call)</span>}
              </li>
            ))}
          </ol>
          {incident.lockdowns?.map((l) => (
            <p key={l.seq} className="small warn-text">
              🔒 {l.principal} was locked: {l.reason}
            </p>
          ))}
        </>
      )}

      <section className="copilot" aria-labelledby="copilot-title" aria-busy={explaining}>
        <h3 id="copilot-title">
          AI co-pilot <span className="small muted">{copilot?.mode === 'llm' ? `· ${copilot.model}` : '· grounded in recorded facts'}</span>
        </h3>
        {explaining && <p className="muted">Reading the flight recorder…</p>}
        {explanation && (
          <>
            {explanation.narrative && (
              <blockquote className="narrative">
                {explanation.narrative}
                <footer className="small muted">Written by {explanation.model}, from the recorded facts only.</footer>
              </blockquote>
            )}
            <p>
              <strong>Why:</strong> {explanation.why}
            </p>
            <p>
              <strong>Risk:</strong> {explanation.risk}
            </p>
            {explanation.next_steps.length > 0 && (
              <>
                <p>
                  <strong>Next steps</strong>
                </p>
                <ul className="next-steps">
                  {explanation.next_steps.map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                </ul>
              </>
            )}
            {explanation.model_error && <p className="small muted">The model was not used: {explanation.model_error}</p>}
          </>
        )}

        {focus && !focus.executed ? (
          <p className="small muted">Gjallarhorn stopped this call before it ran, so no new rule is needed.</p>
        ) : (
          <div className="suggest-bar">
            <button className="btn primary" onClick={suggest} disabled={suggesting || !incident}>
              {suggesting ? 'Replaying recorded traffic…' : suggestions ? 'Suggest again' : 'Suggest a rule'}
            </button>
            <span className="small muted">Every suggestion is replayed against real traffic first.</span>
          </div>
        )}
        {suggestions && (
          <div aria-live="polite">
            <p className="small">{suggestions.message}</p>
            {suggestions.model_error && <p className="small muted">The model was not used: {suggestions.model_error}</p>}
            <ul className="suggestions">
              {(suggestions.suggestions ?? []).map((s) => (
                <SuggestionCard key={s.id} s={s} onApply={apply} applying={applying === s.id} applied={applied === s.id} stale={applied !== null} />
              ))}
            </ul>
          </div>
        )}
      </section>
    </div>
  );
}
