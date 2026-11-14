// Types for the Gjallarhorn control API. They mirror the Go structs in engine/.

export type Plane = 'llm' | 'mcp' | 'a2a';
export type Kind = 'human' | 'agent' | 'service' | 'unknown';

export interface Principal {
  id: string;
  kind: Kind;
  labels?: Record<string, string>;
}

export interface Facts {
  time: string;
  plane: Plane;
  target: string;
  actor: Principal;
  origin: Principal;
  chain: Principal[];
  depth: number;
  credential: Principal;
  ticket_id?: string;
  ticket_problem?: string;
  task_id?: string;
}

export interface Decision {
  effect: 'allow' | 'deny';
  code: string;
  rule_id?: string;
  reason: string;
  monitored?: string[];
  lock_actor?: boolean;
  blocking?: string;
}

export interface Outcome {
  status: string;
  error?: string;
  latency_ms?: number;
  tokens?: number;
}

export type EventType = 'decision' | 'outcome' | 'lockdown' | 'unlock' | 'policy' | 'grant' | 'ticket';

export interface RecEvent {
  seq: number;
  ts: string;
  type: EventType | string;
  request_id?: string;
  facts?: Facts;
  decision?: Decision;
  outcome?: Outcome;
  note?: Record<string, string>;
}

export interface Lockdown {
  principal: string;
  reason: string;
  by: string;
  since: string;
  auto?: boolean;
  rule_id?: string;
  until?: string;
}

export interface Stats {
  events: number;
  last_seq: number;
}

export interface Match {
  planes?: Plane[];
  targets?: string[];
  actors?: string[];
  actor_kinds?: Kind[];
  actor_labels?: Record<string, string>;
  origins?: string[];
  origin_kinds?: Kind[];
  origin_labels?: Record<string, string>;
  via?: string[];
  delegated?: boolean;
  min_depth?: number;
  max_depth?: number;
}

export interface Rule {
  id: string;
  description?: string;
  effect: 'allow' | 'deny';
  mode?: 'enforce' | 'monitor';
  lockdown?: boolean;
  match: Match;
  unless?: Match;
}

export interface Policy {
  version: number;
  default_effect?: 'allow' | 'deny';
  guards?: Record<string, unknown>;
  rules?: Rule[];
}

export interface Step {
  seq: number;
  time: string;
  depth: number;
  parent?: number;
  plane: Plane;
  actor: string;
  target: string;
  chain: string[];
  decision: Decision;
  monitored?: boolean;
  outcome?: Outcome;
  label?: string;
  focus?: boolean;
  executed: boolean;
}

export interface Incident {
  focus: number;
  root: number;
  origin: string;
  agents: string[];
  steps: Step[];
  lockdowns?: { seq: number; time: string; principal: string; reason: string; by: string; rule_id?: string }[];
  start: string;
  end: string;
  blocked: number;
  executed: number;
}

export interface Explanation {
  title: string;
  story: string[];
  why: string;
  risk: string;
  next_steps: string[];
  narrative?: string;
  source: 'llm' | 'grounded';
  model?: string;
  model_error?: string;
}

export interface LabelStats {
  total: number;
  blocked_before: number;
  blocked_after: number;
}

export type SuggestionStatus = 'proven' | 'review' | 'rejected';

export interface Suggestion {
  id: string;
  rule: Rule;
  source: string;
  rationale?: string;
  status: SuggestionStatus;
  verdict: string;
  stops_focus: boolean;
  newly_blocked: number;
  also_catches: number;
  collateral: number;
  collateral_calls?: string[];
  labels?: Record<string, LabelStats>;
}

export interface Suggestions {
  focus: number;
  message: string;
  suggestions: Suggestion[] | null;
  model?: string;
  model_error?: string;
  policy_etag: string;
}

export interface ApplyResult {
  applied: boolean;
  suggestion: Suggestion;
  policy_etag: string;
}

export interface Change {
  seq: number;
  time: string;
  label?: string;
  facts: Facts;
  before: Decision;
  after: Decision;
}

export interface SimReport {
  safe: boolean;
  evaluated: number;
  skipped: number;
  unchanged: number;
  blocked_before: number;
  blocked_after: number;
  newly_blocked: Change[] | null;
  newly_allowed: Change[] | null;
  truncated: boolean;
  denied_by: Record<string, number>;
  labels?: Record<string, LabelStats>;
}

export interface CopilotInfo {
  model?: string;
  mode: 'llm' | 'grounded';
}

export interface Scenario {
  id: string;
  label: 'attack' | 'benign';
  title: string;
  story: string;
}

export interface ScenarioResult {
  id: string;
  label: string;
  title: string;
  enforcing: boolean;
  passed: boolean;
  verdict: string;
  effects: Record<string, number>;
  denials: string[] | null;
  harm_caused: boolean;
  error?: string;
}

export interface TrafficState {
  on: boolean;
  runs: number;
  last?: ScenarioResult;
}
