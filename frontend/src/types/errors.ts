/** Mirrors the Go types in internal/doppler and internal/migration. */

export interface Evidence {
  side: 'source' | 'target' | '';
  token_hint?: string;
  token_kind?: string;
  token_kind_label?: string;
  actor?: string;
  method?: string;
  url?: string;
  status: number;
  status_text?: string;
  body?: string;
  attempts: number;
  retry_after: number;
  latency_ms: number;
}

export type RemedyKind = 'token' | 'pace' | 'decision' | 'recheck' | 'none';

export interface RemedyChoice {
  value: string;
  label: string;
  description: string;
}

export interface Remedy {
  kind: RemedyKind;
  /** Title Case, and never carries a count — the count lives in `detail`. */
  label: string;
  detail: string;
  choices?: RemedyChoice[];
}

export type Severity = 'error' | 'warning';

export interface ErrorClassSummary {
  key: string;
  title: string;
  cause: string;
  severity: Severity;
  remedy: Remedy;

  phase: number;
  phase_name: string;
  resource: string;
  resource_label: string;

  count: number;
  resolved_count: number;
  open_count: number;
  weight: number;

  first_seen: string;
  last_seen: string;
  sample: Evidence;
  projects?: string[];

  resolution?: string;
  resolved_at?: string;
}

export interface ErrorRecord {
  id: string;
  class: string;
  phase: number;
  phase_name: string;
  resource: string;
  resource_label: string;
  item: string;
  project: string;
  config?: string;
  size?: string;
  weight?: number;
  message: string;
  evidence: Evidence;
  occurred_at: string;
  resolved: boolean;
  resolved_at?: string;
  resolution?: string;
}

export interface ErrorsResponse {
  classes: ErrorClassSummary[];
  total: number;
  open: number;
  class_count: number;
  fixable_open: number;
}

export interface ErrorClassResponse {
  class: ErrorClassSummary;
  records: ErrorRecord[];
  total: number;
  truncated: boolean;
}

export interface ResolveRequest {
  remedy?: RemedyKind;
  token?: string;
  pace_ms?: number;
  choice?: string;
  projects?: string[];
  ids?: string[];
  dry_run?: boolean;
}

export interface ResolveResponse {
  status: 'planned' | 'started' | 'resolved';
  class: string;
  item_count: number;
  weight: number;
  regather_configs?: number;
  resolution: string;
  resolved?: number;
}

/** Pre-flight carries the same three parts as a run-time error class. */
export interface CheckScope {
  affected: number;
  total: number;
  unit: string;
  estimated: boolean;
  description: string;
}

export interface CheckFix {
  kind: 'token' | 'recheck' | string;
  label: string;
  field?: string;
  hint?: string;
  blocking: boolean;
}
