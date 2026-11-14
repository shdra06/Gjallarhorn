export function timeAgo(ms: number, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 5) return 'just now';
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h} h ago`;
  return `${Math.round(h / 24)} d ago`;
}

export function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** Human-readable summary of a policy match, e.g. "tool email.send · actor agent:research". */
export function describeMatch(m: Record<string, unknown> | undefined): string[] {
  if (!m) return [];
  const out: string[] = [];
  const list = (k: string, name: string) => {
    const v = m[k];
    if (Array.isArray(v) && v.length) out.push(`${name} ${v.join(', ')}`);
  };
  const labels = (k: string, name: string) => {
    const v = m[k] as Record<string, string> | undefined;
    if (v && Object.keys(v).length) out.push(`${name} ${Object.entries(v).map(([a, b]) => `${a}=${b}`).join(', ')}`);
  };
  list('planes', 'on');
  list('targets', 'target');
  list('actors', 'actor');
  list('actor_kinds', 'actor kind');
  labels('actor_labels', 'actor label');
  list('origins', 'started by');
  list('origin_kinds', 'started by kind');
  labels('origin_labels', 'started by label');
  list('via', 'via');
  if (m.delegated === true) out.push('delegated');
  if (m.delegated === false) out.push('not delegated');
  if (typeof m.min_depth === 'number') out.push(`depth ≥ ${m.min_depth}`);
  if (typeof m.max_depth === 'number') out.push(`depth ≤ ${m.max_depth}`);
  return out;
}
