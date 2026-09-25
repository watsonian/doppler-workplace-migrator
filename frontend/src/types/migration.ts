import type { CheckFix, CheckScope, Evidence } from './errors';

export type CheckStatus = 'pending' | 'running' | 'passed' | 'failed' | 'warning' | 'skipped';

/**
 * A pre-flight check result.
 *
 * A failing check carries the same three parts a run-time error class does —
 * what went wrong, the exchange that proves it, and how to fix it — because they
 * are the same problem caught at different moments, and they should not have to
 * be understood twice.
 */
export interface PreflightCheck {
  id: string;
  name: string;
  status: CheckStatus;
  message?: string;
  detail?: string;
  /** The request the check made and what came back. Present on real failures. */
  evidence?: Evidence;
  /** What this would cost if left unfixed, e.g. how many configs would fail. */
  scope?: CheckScope;
  /** How to clear it before the run starts. */
  fix?: CheckFix;
}
