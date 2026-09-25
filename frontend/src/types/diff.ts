/** Mirrors internal/migration/diff.go, cutover.go and drift.go. */

export type Disposition = 'create' | 'update' | 'match' | 'orphan';

export type ResourceKind =
  | 'workplace_role'
  | 'project_role'
  | 'group'
  | 'group_members'
  | 'service_account'
  | 'project'
  | 'environment'
  | 'config'
  | 'secret'
  | 'webhook'
  | 'project_member'
  | 'trusted_ips';

/**
 * One field that differs, named on both sides.
 *
 * A secret's difference records which fields differ and leaves `was` and `now`
 * empty — the plan is read on screen, screenshotted and exported, and a value
 * here would travel with all three.
 */
export interface FieldChange {
  field: string;
  was: string;
  now: string;
}

export interface Difference {
  kind: ResourceKind;
  disposition: Disposition;
  project?: string;
  config?: string;
  name: string;
  scope?: string;
  changes?: FieldChange[];
  weight?: number;
  /**
   * What a project create brings with it, by kind. A new project is built
   * whole, so its contents are counted here rather than listed as differences.
   */
  contains?: Partial<Record<ResourceKind, number>>;
  /** The target's identifier, where it differs from the source's. */
  target_id?: string;
  /** How the item reads when its name is an identifier: "group Engineering". */
  label?: string;
}

export interface DriftRecord {
  project: string;
  config: string;
  scope: string;
  changed_at: string;
  /** Who made the change. The reason drift is read from logs rather than hashes. */
  changed_by: string;
  summary: string;
}

export interface DiffCounts {
  create: number;
  update: number;
  match: number;
  orphan: number;
  drift: number;
}

export interface CompareResponse {
  status: string;
  counts: DiffCounts;
  /** A first migration, so the interface can present it as one. */
  target_empty: boolean;
  differences: Difference[];
  matched: Record<string, number>;
  drift?: DriftRecord[];
  /**
   * Set when drift could not be determined. Distinct from an empty `drift`,
   * which means it was checked and there was none.
   */
  drift_unavailable?: string;
  error?: string;
}

export type RunMode = 'top_up' | 'cut_over';
export type DriftPolicy = 'overwrite' | 'keep_target';

export interface ConvergeRequest {
  mode?: RunMode;
  delete_orphans?: boolean;
  delete_orphan_configs?: boolean;
  drift?: DriftPolicy;
}

export interface ConvergeResult {
  created: number;
  updated: number;
  deleted: number;
  skipped: number;
  failed: number;
  deferred: number;
  secrets_written: number;
  started_at: string;
  finished_at: string;
}

export interface TrustedIPWork {
  project: string;
  config: string;
  scope: string;
  add?: string[];
  remove?: string[];
  /** True when the mirror removes the allow-all default, closing the config. */
  closes_config: boolean;
}

export interface CutoverPlan {
  trusted_ips: TrustedIPWork[];
  /** Configs needing nothing — the number that says how small this step is. */
  already_matching: number;
  /** The configs behind that count. */
  matching: string[];
  /** Configs whose target ranges could not be read; nothing is planned for them. */
  unreadable: string[];
  /** Source configs the target does not have yet, left out of the plan. */
  not_on_target?: number;
  closing_count: number;
  migrator_cidr?: string;
}

export interface CutoverResult {
  configs_mirrored: number;
  ranges_added: number;
  ranges_removed: number;
  failed: number;
  /** Configs still carrying the migrator's address. Non-empty means unfinished. */
  self_trust_outstanding?: string[];
  migrator_cidr?: string;
  finished_at: string;
}
