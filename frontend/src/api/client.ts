const API_BASE = '/api/v1';

export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = 'APIError';
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const opts: RequestInit = {
    method,
    headers: { 'Content-Type': 'application/json' },
  };
  if (body !== undefined) {
    opts.body = JSON.stringify(body);
  }

  const resp = await fetch(`${API_BASE}${path}`, opts);

  // An error response is not necessarily JSON — a timed-out or proxied request can
  // return HTML or an empty body. Parsing before checking `ok` let that surface as
  // a SyntaxError instead of the actual status.
  let data: unknown;
  try {
    data = await resp.json();
  } catch {
    throw new APIError(
      resp.status,
      resp.ok ? 'Server returned a malformed response' : `Request failed (${resp.status})`,
    );
  }

  if (!resp.ok) {
    const message = (data as { error?: string } | null)?.error;
    throw new APIError(resp.status, message || `Request failed (${resp.status})`);
  }

  return data as T;
}

// --- Configuration ---

export interface ConfigureRequest {
  source_host: string;
  /** Omit to keep the token the server already holds. */
  source_token?: string;
  target_host: string;
  target_token?: string;
  dev_mode: boolean;
  /** Rename the target workplace to match the source. Off unless asked for. */
  migrate_workplace_name?: boolean;
  /** Pause on the first failure of a kind. On unless explicitly disabled. */
  stop_on_new_error_class?: boolean;
}

export interface ConfigureResponse {
  valid: boolean;
  errors?: string[];
  dev_mode: boolean;
}

export function configure(config: ConfigureRequest): Promise<ConfigureResponse> {
  return request('POST', '/configure', config);
}

export interface GetConfigureResponse {
  configured: boolean;
  source_host?: string;
  /** Token type prefix plus last four characters, e.g. "dp.ct.…a1b2". Never the value. */
  source_token_hint?: string;
  has_source_token: boolean;
  target_host?: string;
  target_token_hint?: string;
  has_target_token: boolean;
  /** The held token came from SOURCE_DOPPLER_TOKEN / TARGET_DOPPLER_TOKEN. */
  source_token_from_env?: boolean;
  target_token_from_env?: boolean;
  dev_mode: boolean;
  /** DEV_MODE is set in the server environment, pinning the session to a dry run. */
  dev_mode_forced: boolean;
  migrate_workplace_name: boolean;
  stop_on_new_error_class: boolean;
}

export function getConfiguration(): Promise<GetConfigureResponse> {
  return request('GET', '/configure');
}

// --- Preflight ---

export type { PreflightCheck } from '@/types/migration';
import type { PreflightCheck } from '@/types/migration';

export interface PreflightResponse {
  checks: PreflightCheck[];
  passed: boolean;
  /** Warnings do not block the migration, but are worth acting on before it runs. */
  has_warnings: boolean;
  dev_mode: boolean;
}

export function runPreflight(): Promise<PreflightResponse> {
  return request('POST', '/preflight');
}

// --- Gather ---

export interface GatherProgress {
  resource: string;
  current: number;
  total: number;
  message: string;
}

export interface GatherResponse {
  status: string;
  summary: PlanSummary;
  error?: string;
}

/** Starts the gather and returns immediately; progress arrives over SSE. */
export function startGather(): Promise<{ status: string }> {
  return request('POST', '/gather');
}

export interface GatherStatusResponse {
  gathering: boolean;
  status?: 'complete' | 'failed';
  summary?: PlanSummary;
  error?: string;
  /** What the source token was refused, and so is left out. */
  unavailable?: UnavailableResource[] | null;
}

/** A kind of thing the source token could not read, and so is not migrated. */
export interface UnavailableResource {
  resource: string;
  label: string;
  consequence: string;
  permission: string;
  message: string;
  where: number;
  examples?: string[];
}

/** Fallback for a client that missed the SSE completion event. */
export function getGatherStatus(): Promise<GatherStatusResponse> {
  return request('GET', '/gather');
}

// --- Plan ---

export interface PlanSummary {
  project_count: number;
  environment_count: number;
  config_count: number;
  secret_count: number;
  role_count: number;
  group_count: number;
  webhook_count: number;
  service_account_count: number;
  trusted_ip_count: number;
  change_request_policy_count: number;
  manual_step_count: number;
}

export interface PlanResponse {
  summary: PlanSummary;
  projects: ProjectPlan[];
  workplace: WorkplacePlan;
  manual_steps: ManualStep[];
  user_check: UserCheckResult;
}

export interface WorkplacePlan {
  settings: boolean;
  workplace_roles: string[];
  project_roles: string[];
  groups: GroupPlanItem[];
  service_accounts: string[];
  change_request_policies: ChangeRequestPolicyPlanItem[];
}

export interface ChangeRequestPolicyPlanItem {
  name: string;
  rule_count: number;
  all_projects: boolean;
  /** Number of individually targeted projects; 0 with all_projects means workplace-wide. */
  project_count: number;
}

export interface GroupPlanItem {
  name: string;
  member_count: number;
}

export interface ProjectPlan {
  name: string;
  description: string;
  environments: EnvironmentPlan[];
  configs: ConfigPlanItem[];
  webhooks: WebhookPlan[];
  members: MemberPlan[];
}

export interface EnvironmentPlan {
  name: string;
  slug: string;
}

export interface ConfigPlanItem {
  name: string;
  environment: string;
  root: boolean;
  secret_count: number;
  trusted_ips: number;
  locked: boolean;
  inheritable: boolean;
  inherits?: string[];
}

export interface WebhookPlan {
  url: string;
  enabled: boolean;
}

export interface MemberPlan {
  type: string;
  slug: string;
  role: string;
}

export interface ManualStep {
  category: string;
  title: string;
  description: string;
  items: ManualStepItem[] | null;
}

export interface ManualStepItem {
  name: string;
  detail: string;
  context?: Record<string, string>;
}

export interface UserCheckResult {
  required: string[] | null;
  existing: string[] | null;
  missing: string[] | null;
}

export function getPlan(): Promise<PlanResponse> {
  return request('GET', '/plan');
}

// --- Manual Steps ---

export interface ManualStepsResponse {
  manual_steps: ManualStep[];
  user_check: UserCheckResult;
}

export function getManualSteps(): Promise<ManualStepsResponse> {
  return request('GET', '/report/manual-steps');
}

// --- Service Token Report ---

export interface ServiceToken {
  name: string;
  slug: string;
  created_at: string;
  expires_at?: string | null;
  project: string;
  config: string;
  environment: string;
  access: string;
}

export interface ServiceAccountToken {
  name: string;
  slug: string;
  created_at: string;
  last_seen_at?: string | null;
  expires_at?: string | null;
  service_account: string;
}

export interface ServiceTokenReportResponse {
  service_tokens: ServiceToken[];
  service_account_tokens: ServiceAccountToken[];
}

export function getServiceTokenReport(): Promise<ServiceTokenReportResponse> {
  return request('GET', '/report/service-tokens');
}

// --- Service Token Issuance ---

export type TokenKind = 'service_token' | 'service_account_token';
/** 'deferred' is "later": set aside now, undecided again at the next run. */
export type TokenDisposition = 'pending' | 'issued' | 'to_recreate' | 'not_needed' | 'deferred';

export interface TokenRecord {
  key: string;
  kind: TokenKind;
  project?: string;
  config?: string;
  environment?: string;
  service_account?: string;
  /** The account's name. Its slug is a UUID, and differs on the target. */
  service_account_name?: string;
  name: string;
  access?: string;
  source_expires_at?: string | null;
  disposition: TokenDisposition;
  issued_at?: string | null;
  target_slug?: string;
  issued_expires_at?: string | null;
  expiry_shortened?: boolean;
  note?: string;
  /** When the attempt behind a failure note was made. */
  failed_at?: string | null;
  preexisting_on_target?: boolean;
}

export interface TokenCounts {
  total: number;
  issued: number;
  to_recreate: number;
  not_needed: number;
  pending: number;
  /** Set aside for later. Not outstanding now, but cutover waits on them. */
  deferred?: number;
}

export interface TokenGate {
  counts: TokenCounts;
  blocked: boolean;
  reason?: string;
}

export interface TokensResponse {
  records: TokenRecord[];
  counts: TokenCounts;
  gate: TokenGate;
  /** Tokens held back because their project has not been migrated yet. */
  not_yet_migrated?: number;
}

/**
 * A token value, on its way to being shown once.
 *
 * Nothing stores this. There is no endpoint that returns it a second time —
 * losing it is what reroll is for — so it must not be written to component
 * state that outlives the dialog, or to storage of any kind.
 */
export interface IssuedToken {
  key: string;
  kind: TokenKind;
  name: string;
  scope: string;
  access?: string;
  value: string;
  expires_at?: string | null;
  expiry_shortened: boolean;
  issued_at: string;
  replaced_at?: string | null;
}

export interface IssueResponse {
  token: IssuedToken;
  counts: TokenCounts;
  gate: TokenGate;
}

export function getTokens(): Promise<TokensResponse> {
  return request('GET', '/tokens');
}

/** Creates the token on the target and returns its value, once. */
export function issueToken(key: string): Promise<IssueResponse> {
  return request('POST', '/tokens/issue', { key });
}

/** Deletes what the migrator created and issues a replacement. */
export function rerollToken(key: string): Promise<IssueResponse> {
  return request('POST', '/tokens/reroll', { key });
}

/** Records a decision to handle this token outside the migrator. */
export function skipToken(key: string, notNeeded: boolean): Promise<TokensResponse> {
  return request('POST', '/tokens/skip', { key, not_needed: notNeeded });
}

export function skipAllTokens(): Promise<TokensResponse & { moved: number }> {
  return request('POST', '/tokens/skip-all');
}

export function undoTokenSkip(key: string): Promise<TokensResponse> {
  return request('POST', '/tokens/undo', { key });
}

/** Sets a token aside until the next run, which offers it again. */
export function deferToken(key: string): Promise<TokensResponse> {
  return request('POST', '/tokens/defer', { key });
}

/** Looks for tokens already on the target under the same name. */
export function detectExistingTokens(): Promise<TokensResponse & { detected: number }> {
  return request('POST', '/tokens/detect');
}

export const tokenWorklistURL = `${API_BASE}/tokens/worklist.csv`;

/** The whole migration as a page to keep, and the same report as data. */
export const finalReportURL = `${API_BASE}/report/final.html`;
export const finalReportJSONURL = `${API_BASE}/report/final.json`;

// --- Summary Report ---

export interface SummaryResponse {
  summary: PlanSummary;
  checkpoint: CheckpointData | null;
  manual_step_count: number;
  /** What the last run covered. Absent before any run in this session. */
  run?: RunInfo | null;
  /** The whole source, so a partial's summary can say "3 of 92". */
  source_summary?: PlanSummary;
  /** Set while the target holds only what a partial run moved. */
  cutover_blocked?: boolean;
  /** The token page's state; tokens are not counted among the manual steps. */
  token_counts?: TokenCounts;
  /** What is left by hand, by kind. */
  manual_steps?: { category: string; count: number }[];
  /** Where the migration stands between its full run and cutover. */
  standby?: Standby;
}

export function getSummary(): Promise<SummaryResponse> {
  return request('GET', '/report/summary');
}

// --- Migration ---

export interface MigrateProgress {
  phase: number;
  name: string;
  resource: string;
  item: string;
  status: 'started' | 'completed' | 'skipped' | 'failed';
  current: number;
  total: number;
  error?: string;
  /** Why a phase was skipped. */
  reason?: string;
}

export interface MigrateResponse {
  status: 'completed' | 'failed';
  error?: string;
}

export interface MigrationStatus {
  migrating: boolean;
  paused: boolean;
  phase?: string;
  migration_result?: string;
  error?: string;
  /** Where each phase stands, for a page that missed the events. */
  phases?: Record<string, { status: string; reason?: string }>;
  /** How the current or last run split its projects. */
  run?: RunInfo | null;
}

export interface CheckpointData {
  started_at: string;
  updated_at: string;
  phases: Record<string, PhaseSummary>;
  completed: boolean;
  error?: string;
}

export interface PhaseSummary {
  name: string;
  status: string;
  items: Record<string, { status: string; reason?: string }>;
  started_at?: string;
  done_at?: string;
}

export function startMigration(): Promise<MigrateResponse> {
  return request('POST', '/migrate');
}

export function pauseMigration(): Promise<{ status: string }> {
  return request('POST', '/migrate/pause');
}

export function resumeMigration(): Promise<{ status: string }> {
  return request('POST', '/migrate/resume');
}

export function getMigrationStatus(): Promise<MigrationStatus> {
  return request('GET', '/status');
}

export function getCheckpoint(): Promise<CheckpointData> {
  return request('GET', '/checkpoint');
}

// --- SSE ---

export type SSEEventHandler = (type: string, data: unknown) => void;

export interface SSESubscription {
  unsubscribe: () => void;
  connected: Promise<void>;
}

export function subscribeSSE(onEvent: SSEEventHandler): SSESubscription {
  const eventSource = new EventSource(`${API_BASE}/events`);

  let resolveConnected: () => void;
  const connected = new Promise<void>((resolve) => {
    resolveConnected = resolve;
  });

  eventSource.addEventListener('preflight_check', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('preflight_check', data);
  });

  eventSource.addEventListener('preflight_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('preflight_complete', data);
  });

  eventSource.addEventListener('gather_progress', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('gather_progress', data);
  });

  eventSource.addEventListener('gather_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('gather_complete', data);
  });

  eventSource.addEventListener('migrate_progress', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('migrate_progress', data);
  });

  eventSource.addEventListener('phase_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('phase_complete', data);
  });

  // A rate-limit pause starting, moving, or ending, on either side.
  eventSource.addEventListener('rate_limit', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('rate_limit', data);
  });

  // Where each token stands against its limits, throttled by the server.
  eventSource.addEventListener('rate_status', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('rate_status', data);
  });

  // A full run after a partial runs in two stages; this says which it is on.
  eventSource.addEventListener('migration_stage', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('migration_stage', data);
  });

  eventSource.addEventListener('migration_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('migration_complete', data);
  });

  // Re-run lifecycle. Fixing an error class kicks off a run of its own, and the
  // triage view has to follow it the same way the migration view follows the
  // first run.
  eventSource.addEventListener('errors_changed', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('errors_changed', data);
  });

  eventSource.addEventListener('rerun_progress', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('rerun_progress', data);
  });

  eventSource.addEventListener('rerun_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('rerun_complete', data);
  });

  eventSource.addEventListener('compare_progress', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('compare_progress', data);
  });

  eventSource.addEventListener('compare_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('compare_complete', data);
  });

  eventSource.addEventListener('converge_progress', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('converge_progress', data);
  });

  eventSource.addEventListener('converge_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('converge_complete', data);
  });

  eventSource.addEventListener('cutover_progress', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('cutover_progress', data);
  });

  eventSource.addEventListener('cutover_complete', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('cutover_complete', data);
  });

  eventSource.addEventListener('rate_limit', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('rate_limit', data);
  });

  // Where each token stands against its limits, throttled by the server.
  eventSource.addEventListener('rate_status', (e) => {
    const data = JSON.parse((e as MessageEvent).data);
    onEvent('rate_status', data);
  });

  eventSource.addEventListener('connected', () => {
    onEvent('connected', {});
  });

  // Resolve on native 'open' (HTTP connection established) rather than
  // the custom 'connected' SSE event, which may be buffered by proxies.
  eventSource.onopen = () => {
    resolveConnected();
  };

  eventSource.onerror = () => {
    // Resolve connected even on error so callers don't hang
    resolveConnected();
    onEvent('error', { message: 'SSE connection lost' });
  };

  return {
    unsubscribe: () => eventSource.close(),
    connected,
  };
}

// --- Errors ---

export type {
  ErrorsResponse,
  ErrorClassResponse,
  ErrorClassSummary,
  ErrorRecord,
  Evidence,
  Remedy,
  RemedyKind,
  ResolveRequest,
  ResolveResponse,
} from '@/types/errors';

import type {
  ErrorsResponse as ErrorsResp,
  ErrorClassResponse as ErrorClassResp,
  ResolveRequest as ResolveReq,
  ResolveResponse as ResolveResp,
} from '@/types/errors';

/** Every error class in the current or most recent run, worst first. */
export function getErrors(): Promise<ErrorsResp> {
  return request('GET', '/errors');
}

/**
 * The instances behind one class. Filtering by project is how a class of several
 * hundred becomes a list someone can actually review and choose from.
 */
export function getErrorClass(
  classKey: string,
  opts: { projects?: string[]; openOnly?: boolean } = {},
): Promise<ErrorClassResp> {
  const params = new URLSearchParams();
  if (opts.projects?.length) params.set('project', opts.projects.join(','));
  if (opts.openOnly) params.set('open', '1');
  const query = params.toString();
  return request('GET', `/errors/${encodeURIComponent(classKey)}${query ? `?${query}` : ''}`);
}

/**
 * Applies a class's remedy to a selection.
 *
 * With `dry_run` it reports what would be touched and changes nothing — which is
 * what the interface asks before it asks the operator to agree.
 */
export function resolveErrorClass(classKey: string, req: ResolveReq): Promise<ResolveResp> {
  return request('POST', `/errors/${encodeURIComponent(classKey)}/resolve`, req);
}

/** The export URL. Served as a download, so it is navigated to rather than fetched. */
export const errorsExportURL = `${API_BASE}/errors/export.csv`;

// --- Compare, converge and cutover ---

export type {
  CompareResponse,
  ConvergeRequest,
  ConvergeResult,
  CutoverPlan,
  CutoverResult,
  Difference,
  DiffCounts,
  Disposition,
  DriftRecord,
  ResourceKind,
  TrustedIPWork,
} from '@/types/diff';

import type {
  CompareResponse as CompareResp,
  ConvergeRequest as ConvergeReq,
  CutoverPlan as CutoverPlanT,
} from '@/types/diff';

/** Starts the comparison; the result arrives over SSE. */
export function startCompare(): Promise<{ status: string }> {
  return request('POST', '/compare');
}

/** Fallback for a client that missed the SSE completion event. */
export function getCompare(): Promise<CompareResp | { status: string }> {
  return request('GET', '/compare');
}

/** Applies the diff. Progress and completion arrive over SSE. */
export function startConverge(req: ConvergeReq = {}): Promise<{ status: string; counts: unknown }> {
  return request('POST', '/converge', req);
}

/**
 * Plans the cutover without doing any of it, so the scope can be stated before
 * anyone agrees to it.
 */
export function planCutover(
  migratorCIDR = '',
): Promise<{ status: string; plan: CutoverPlanT; token_gate?: TokenGate; partial_only?: boolean }> {
  return request('POST', '/cutover', { migrator_cidr: migratorCIDR, dry_run: true });
}

/** Runs the cutover. */
export function startCutover(migratorCIDR = ''): Promise<{ status: string; plan: CutoverPlanT }> {
  return request('POST', '/cutover', { migrator_cidr: migratorCIDR });
}

// --- Scope ---

export type ScopeMode = 'full' | 'partial';

export interface Scope {
  mode: ScopeMode;
  projects?: string[];
}

export interface IncludedResource {
  kind: 'group' | 'service_account' | 'project_role' | 'workplace_role';
  key: string;
  name: string;
  reasons: string[];
}

export interface ScopeResolution {
  mode: ScopeMode;
  projects: string[];
  chosen: string[];
  added?: { project: string; because: string }[];
  included?: IncludedResource[];
  deferred_projects: number;
  deferred_groups: number;
  deferred_roles: number;
  deferred_service_accounts: number;
  deferred_policies: number;
  defers_workplace_settings: boolean;
  policies_affected?: string[];
  total_projects: number;
  configs: number;
  secrets: number;
}

export interface RunPlan {
  resolution: ScopeResolution;
  /** In scope and not yet migrated: stage one creates these. */
  remaining: string[] | null;
  /** In scope and moved by an earlier run: stage two brings these up to date. */
  previously_migrated: string[] | null;
  previously_migrated_at?: string | null;
}

export interface ScopeProject {
  slug: string;
  name: string;
  configs: number;
  secrets: number;
  migrated_at?: string | null;
}

export interface ScopeResponse {
  scope: Scope;
  run: RunPlan;
  projects: ScopeProject[];
  cutover_blocked: boolean;
  user_check?: UserCheckResult;
  /** Set when the next full-scope run is a top-up: which one, and since when. */
  top_up?: { number: number; since?: string | null } | null;
}

export interface ConvergeTotals {
  created: number;
  updated: number;
  deleted: number;
  skipped: number;
  failed: number;
  secrets_written: number;
  /** What the comparison found already matching. */
  current?: number;
}

export interface RunInfo {
  mode: ScopeMode;
  resolution: ScopeResolution;
  remaining: number;
  previously_migrated?: string[] | null;
  previously_migrated_at?: string | null;
  /** Earlier projects no longer on the target, migrated again in stage one. */
  reintroduced?: string[] | null;
  stage: 'remaining' | 'previously_migrated' | 'done';
  top_up?: ConvergeTotals | null;
  top_up_error?: string;
}

export function getScope(): Promise<ScopeResponse> {
  return request('GET', '/scope');
}

export function setScope(scope: Scope): Promise<ScopeResponse> {
  return request('POST', '/scope', scope);
}

export interface RunOptions {
  delete_orphans: boolean;
  delete_orphan_configs: boolean;
  drift: 'overwrite' | 'keep_target';
}

/**
 * Stores the Review page's choices on the server, where the run reads them.
 * They used to ride along as navigation state that nothing downstream read.
 */
export function setRunOptions(opts: RunOptions): Promise<{ status: string }> {
  return request('POST', '/run-options', opts);
}

/** Resolves a choice without storing it, for the picker's live dependency panel. */
export function previewScope(scope: Scope): Promise<{ resolution: ScopeResolution }> {
  return request('POST', '/scope/preview', scope);
}

// --- Rate limits ---

export type RateLimitCategory = 'read' | 'secret_read' | 'write';

/** A pause in progress because one side of the migration is rate limiting it. */
export interface RateWait {
  side: 'source' | 'target';
  category: RateLimitCategory;
  resume_at: string;
  /** The instance answered 429, rather than the known budget running out. */
  refused: boolean;
}

/** The event form, which also announces a pause ending. */
export interface RateLimitEvent extends RateWait {
  waiting: boolean;
}

/** Pauses already in progress, for a page opened in the middle of one. */
/** One kind of request's budget for a token. */
export interface RateCategoryStatus {
  category: RateLimitCategory;
  remaining: number;
  limit: number;
  reset_at: string;
  /** Fraction of the limit spent, 0 to 1. */
  used: number;
}

/** Where one token stands against its rate limits. */
export interface RateSideStatus {
  side: 'source' | 'target';
  categories: RateCategoryStatus[];
  /** ok under 70%, warn to 99%, limited at 100%; empty before any request. */
  level: '' | 'ok' | 'warn' | 'limited';
  used: number;
  reset_at: string;
}

export function getRateLimits(): Promise<{ waits: RateWait[]; status?: RateSideStatus[] }> {
  return request('GET', '/rate-limits');
}

// --- Waiting to cut over ---

export type StandbyRunKind = 'full' | 'partial' | 'top_up';

export interface StandbyRun {
  kind: StandbyRunKind;
  /** Which top-up, counting from 1. */
  number?: number;
  at: string;
  projects: number;
  created: number;
  updated: number;
  failed: number;
  /** What the run wrote, by resource type. Absent on runs recorded before it was kept. */
  changes?: RunChanges | null;
}

export interface RunChanges {
  groups: ChangeGroup[];
}

export interface ChangeGroup {
  label: string;
  created: number;
  updated: number;
  deleted: number;
  migrated: number;
  failed: number;
  items: { action: 'created' | 'updated' | 'deleted' | 'migrated' | 'failed'; item: string }[];
  /** Items past the cap. The counts stay exact. */
  more?: number;
}

export interface Standby {
  /** False before Configure, when only the history on disk is known. */
  configured: boolean;
  /** The source has been read since the server started; cutover plans from it. */
  gathered: boolean;
  source_host?: string;
  target_host?: string;
  full_done: boolean;
  /** From the end of the full run until cutover: the one migration in progress. */
  in_flight: boolean;
  waiting: boolean;
  waiting_since?: string | null;
  cutover_at?: string | null;
  full_at?: string | null;
  last_synced_at?: string | null;
  top_ups: number;
  last_run?: StandbyRun | null;
  /** Newest first. */
  runs: StandbyRun[];
  tokens_pending: number;
  /** The part of tokens_pending set aside for later. */
  tokens_deferred?: number;
  open_errors: number;
  manual: { category: string; title: string; count: number }[];
}

export function getStandby(): Promise<Standby> {
  return request('GET', '/standby');
}

/**
 * The choice at the end of a full run or a top-up: wait and top up, cut over,
 * or finish when cutover has nothing to change.
 */
export function setStandby(action: 'wait' | 'cut_over' | 'finish'): Promise<Standby> {
  return request('POST', '/standby', { action });
}

/** Records a top-up whose comparison found nothing to change. */
export function recordNoChangeCheck(): Promise<Standby> {
  return request('POST', '/standby/check');
}

