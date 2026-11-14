import type {
  ApplyResult,
  CopilotInfo,
  Explanation,
  Incident,
  Lockdown,
  Policy,
  Principal,
  RecEvent,
  Rule,
  Scenario,
  ScenarioResult,
  SimReport,
  Stats,
  Step,
  Suggestions,
  TrafficState,
} from './types';

/** An error from the API, with the HTTP status and any details the server sent. */
export class ApiError extends Error {
  readonly status: number;
  readonly details: string[];
  readonly body: unknown;
  constructor(status: number, message: string, details: string[] = [], body: unknown = undefined) {
    super(message);
    this.status = status;
    this.details = details;
    this.body = body;
  }
}

export interface Reply<T> {
  data: T;
  status: number;
  etag: string | null;
}

/**
 * Client for the control API. The console is served from the same origin as the API, so paths
 * are relative. The admin token travels only in the Authorization header.
 */
export class Api {
  readonly base: string;
  private readonly token: string;
  private readonly operator: string;

  constructor(token: string, base = '', operator = 'console') {
    this.token = token;
    this.base = base;
    this.operator = operator;
  }

  headers(extra: Record<string, string> = {}): Record<string, string> {
    return {
      Authorization: `Bearer ${this.token}`,
      'Content-Type': 'application/json',
      'X-Operator': this.operator,
      ...extra,
    };
  }

  async request<T>(method: string, path: string, body?: unknown, extra: Record<string, string> = {}, okStatuses: number[] = []): Promise<Reply<T>> {
    let res: Response;
    try {
      res = await fetch(this.base + path, {
        method,
        headers: this.headers(extra),
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    } catch (e) {
      throw new ApiError(0, 'Cannot reach Gjallarhorn. Is the control API running?', [String(e)]);
    }
    const text = await res.text();
    let data: unknown = undefined;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = text;
      }
    }
    if (!res.ok && !okStatuses.includes(res.status)) {
      const err = (data ?? {}) as { error?: string; details?: string[] };
      const msg = typeof err === 'object' && err.error ? err.error : `${method} ${path} failed (${res.status})`;
      throw new ApiError(res.status, msg, err.details ?? [], data);
    }
    return { data: data as T, status: res.status, etag: res.headers.get('ETag') };
  }

  async get<T>(path: string): Promise<T> {
    return (await this.request<T>('GET', path)).data;
  }

  async post<T>(path: string, body: unknown): Promise<T> {
    return (await this.request<T>('POST', path, body)).data;
  }

  health() {
    return this.get<{ status: string; version: string; events: number; lockdowns: number }>('/v1/health');
  }
  principals() {
    return this.get<Principal[]>('/v1/principals');
  }
  stats() {
    return this.get<Stats>('/v1/stats');
  }
  events(after: number, limit = 5000) {
    return this.get<RecEvent[]>(`/v1/events?after=${after}&limit=${limit}`);
  }
  lockdowns() {
    return this.get<Lockdown[]>('/v1/lockdowns');
  }
  lock(principal: string, reason: string) {
    return this.post<Lockdown>('/v1/lockdowns', { principal, reason });
  }
  async unlock(principal: string) {
    await this.request('DELETE', `/v1/lockdowns/${encodeURIComponent(principal)}`);
  }
  async mode() {
    return (await this.get<{ enforcing: boolean }>('/v1/mode')).enforcing;
  }
  async setMode(enforcing: boolean) {
    return (await this.request<{ enforcing: boolean }>('PUT', '/v1/mode', { enforcing })).data.enforcing;
  }
  policy() {
    return this.request<Policy>('GET', '/v1/policy');
  }
  savePolicy(p: unknown, etag: string | null) {
    return this.request<Policy>('PUT', '/v1/policy', p, etag ? { 'If-Match': etag } : {});
  }
  validatePolicy(p: unknown) {
    return this.post<{ valid: boolean; errors: string[] | null }>('/v1/policy/validate', p);
  }
  simulate(p: unknown) {
    return this.post<SimReport>('/v1/simulate', { policy: p, max_changes: 200 });
  }
  incidents(limit = 50) {
    return this.get<Step[]>(`/v1/incidents?limit=${limit}`);
  }
  incident(seq: number) {
    return this.get<Incident>(`/v1/incidents/${seq}`);
  }
  copilot() {
    return this.get<CopilotInfo>('/v1/copilot');
  }
  explain(seq: number) {
    return this.post<Explanation>('/v1/copilot/explain', { seq });
  }
  suggest(seq: number) {
    return this.post<Suggestions>('/v1/copilot/suggest', { seq });
  }
  apply(seq: number, rule: Rule, etag: string, acceptReview = false) {
    return this.request<ApplyResult>('POST', '/v1/copilot/apply', { seq, rule, accept_review: acceptReview }, { 'If-Match': `"${etag}"` }, [409]);
  }

  // Demo traffic driver (only present in demo environments).
  scenarios() {
    return this.get<Scenario[]>('/demo/scenarios');
  }
  runScenario(id: string) {
    return this.post<ScenarioResult>(`/demo/scenarios/${encodeURIComponent(id)}/run`, {});
  }
  traffic() {
    return this.get<TrafficState>('/demo/traffic');
  }
  async setTraffic(on: boolean) {
    return (await this.request<TrafficState>('PUT', '/demo/traffic', { on })).data;
  }
}
