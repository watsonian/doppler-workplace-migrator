import { useState, useEffect, useRef, useCallback } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { ArrowRight } from 'lucide-react';
import {
  Block,
  Button,
  Callout,
  FooterBar,
  PageHeading,
  Pill,
  ProgressBar,
  StatusIcon,
} from '@/components/ui/primitives';
import { startGather, getGatherStatus, subscribeSSE } from '@/api/client';
import type { UnavailableResource } from '@/api/client';
import type { GatherProgress, PlanSummary } from '@/api/client';
import { isInFlight, useStandby } from '@/hooks/useStandby';

// Resource display names and order
// Every resource the migrator reads. Listed in full, including ones that come back
// empty — "no change request policies" tells the operator something, whereas a
// missing row just looks like the tool forgot.
const RESOURCE_LABELS: Record<string, string> = {
  workplace: 'Workplace Settings',
  users: 'Workplace Users',
  workplace_roles: 'Custom Workplace Roles',
  project_roles: 'Custom Project Roles',
  groups: 'Groups & Members',
  service_accounts: 'Service Accounts',
  service_account_tokens: 'Service Account Tokens',
  service_account_identities: 'Service Account Identities',
  change_request_policies: 'Change Request Policies',
  integrations: 'Integrations',
  projects: 'Projects',
  environments: 'Environments',
  configs: 'Configs',
  secrets: 'Secrets',
  config_inheritance: 'Config Inheritance',
  trusted_ips: 'Trusted IPs',
  webhooks: 'Webhooks',
  project_members: 'Project Members',
  service_tokens: 'Service Tokens',
  rotated_secrets: 'Rotated Secrets',
  personal_configs: 'Personal Configs',
};

// Resources that are read for the report but never migrated, so an empty result is
// the expected case rather than something to note.
const REPORT_ONLY = new Set([
  'integrations',
  'service_account_tokens',
  'service_tokens',
  'rotated_secrets',
  'personal_configs',
]);

const RESOURCE_ORDER = [
  'workplace',
  'users',
  'workplace_roles',
  'project_roles',
  'groups',
  'service_accounts',
  'service_account_identities',
  'projects',
  'environments',
  'configs',
  'secrets',
  'config_inheritance',
  'trusted_ips',
  'webhooks',
  'project_members',
  'change_request_policies',
  'integrations',
  'service_tokens',
  'service_account_tokens',
  'rotated_secrets',
  'personal_configs',
];

interface ResourceProgress {
  current: number;
  total: number;
  message: string;
  done: boolean;
  /** Reported, but there was nothing of this kind to migrate. */
  empty: boolean;
}

export default function GatheringPage() {
  const navigate = useNavigate();
  const { standby } = useStandby();
  const inFlight = isInFlight(standby);
  // Where to go once gathered, when something other than a run sent us here —
  // the waiting page's Cut Over, after a restart.
  const next = (useLocation().state as { next?: string } | null)?.next;

  const [resources, setResources] = useState<Record<string, ResourceProgress>>({});
  const [gathering, setGathering] = useState(false);
  const [completed, setCompleted] = useState(false);
  const [summary, setSummary] = useState<PlanSummary | null>(null);
  const [unavailable, setUnavailable] = useState<UnavailableResource[]>([]);
  const [error, setError] = useState<string | null>(null);

  const hasStarted = useRef(false);
  const unsubRef = useRef<(() => void) | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const completionHandledRef = useRef(false);

  const cleanupSSE = useCallback(() => {
    if (unsubRef.current) {
      unsubRef.current();
      unsubRef.current = null;
    }
  }, []);

  const stopPolling = useCallback(() => {
    if (pollRef.current !== null) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  const handleCompletion = useCallback(
    (status: string, resultSummary?: PlanSummary, errorMsg?: string, refused?: UnavailableResource[] | null) => {
      if (completionHandledRef.current) return;
      completionHandledRef.current = true;

      if (status === 'complete' && resultSummary) {
        setSummary(resultSummary);
        setUnavailable(refused ?? []);
        setCompleted(true);
      } else {
        setError(errorMsg || 'Gathering failed');
      }
      setGathering(false);
      stopPolling();
      cleanupSSE();
    },
    [stopPolling, cleanupSSE],
  );

  const startPolling = useCallback(() => {
    pollRef.current = setInterval(async () => {
      try {
        const status = await getGatherStatus();
        if (!status.gathering && status.status) {
          handleCompletion(status.status, status.summary, status.error, status.unavailable);
        }
      } catch {
        // Ignore polling errors; SSE is the primary channel.
      }
    }, 3000);
  }, [handleCompletion]);

  const handleSSEEvent = useCallback((type: string, data: unknown) => {
    if (type === 'gather_progress') {
      const progress = data as GatherProgress;
      setResources((prev) => ({
        ...prev,
        [progress.resource]: {
          current: progress.current,
          total: progress.total,
          message: progress.message,
          // A zero total is a finished result, not an in-flight one: the resource
          // was looked for and there was none of it.
          done: progress.total === 0 || progress.current >= progress.total,
          empty: progress.total === 0,
        },
      }));
    } else if (type === 'gather_complete') {
      const result = data as {
        status: string;
        summary?: PlanSummary;
        error?: string;
        unavailable?: UnavailableResource[] | null;
      };
      handleCompletion(result.status, result.summary, result.error, result.unavailable);
    }
  }, [handleCompletion]);

  const runGather = useCallback(async () => {
    setGathering(true);
    setCompleted(false);
    setError(null);
    setResources({});
    setSummary(null);
    setUnavailable([]);
    completionHandledRef.current = false;

    const sse = subscribeSSE(handleSSEEvent);
    unsubRef.current = sse.unsubscribe;

    try {
      // Returns as soon as the gather starts. The outcome arrives over SSE, with
      // polling as the fallback for a dropped stream — the gather itself keeps
      // running on the server either way.
      await startGather();
      startPolling();
    } catch (err) {
      setError((err as Error).message);
      setGathering(false);
      cleanupSSE();
    }
  }, [handleSSEEvent, startPolling, cleanupSSE]);

  // Auto-start gathering on mount
  useEffect(() => {
    if (!hasStarted.current) {
      hasStarted.current = true;
      runGather();
    }

    return () => {
      stopPolling();
      cleanupSSE();
    };
  }, [runGather, stopPolling, cleanupSSE]);


  // Calculate overall progress
  const doneCount = RESOURCE_ORDER.filter((r) => resources[r]?.done).length;
  const totalResources = RESOURCE_ORDER.length;

  const emptyCount = RESOURCE_ORDER.filter((r) => resources[r]?.empty).length;
  const reportOnlyCount = RESOURCE_ORDER.filter((r) => REPORT_ONLY.has(r)).length;

  return (
    <div className="mx-auto w-full max-w-[720px] px-6 py-6">
      <PageHeading
        title="Gathering Data"
        subtitle={
          completed
            ? `${totalResources} resource types read from the source workplace.`
            : 'Reading the source workplace. Nothing is written yet.'
        }
      />

      {(gathering || completed) && (
        <ProgressBar
          className="mx-auto mt-3 max-w-[300px]"
          value={((completed ? totalResources : doneCount) / totalResources) * 100}
          label="Gather progress"
        />
      )}

      <div className="mt-5 flex flex-col gap-3.5">
        {error && (
          <Callout tone="error">
            <p className="mb-2">{error}</p>
            <Button
              variant="secondary"
              onClick={() => {
                hasStarted.current = false;
                runGather();
              }}
            >
              Try Again
            </Button>
          </Callout>
        )}

        <Block>
          {RESOURCE_ORDER.map((key) => {
            const rp = resources[key];
            const label = RESOURCE_LABELS[key] || key;
            const isEmpty = rp?.empty;
            const isDone = rp?.done;

            return (
              <div
                key={key}
                className={`flex items-center gap-2.5 border-b border-gray-75 px-3.5 py-2 text-[13px] last:border-b-0 ${
                  isEmpty ? 'opacity-50' : !rp ? 'opacity-50' : ''
                }`}
              >
                <StatusIcon
                  status={isEmpty ? 'skipped' : isDone ? 'completed' : rp ? 'running' : 'pending'}
                />
                <span className="min-w-0 flex-1 truncate">{label}</span>

                {isEmpty ? (
                  /* An empty resource keeps its row. "No change request policies"
                     tells the operator something; a missing row just looks like
                     the tool forgot to check. */
                  <Pill tone="skipped">
                    {REPORT_ONLY.has(key) ? 'None found' : 'None found — nothing to migrate'}
                  </Pill>
                ) : isDone ? (
                  <span className="shrink-0 text-gray-500">
                    {rp.total > 0 ? rp.total.toLocaleString() : rp.message}
                  </span>
                ) : rp ? (
                  <span className="shrink-0 truncate text-gray-500">{rp.message}</span>
                ) : (
                  <span className="shrink-0 text-gray-400">Queued</span>
                )}
              </div>
            );
          })}
        </Block>

        {completed && unavailable.length > 0 && (
          <Callout tone="warning">
            <span className="font-semibold text-gray-900">
              The source token was refused {unavailable.length === 1 ? 'one kind' : `${unavailable.length} kinds`} of
              thing, so {unavailable.length === 1 ? 'it is' : 'they are'} not migrated.
            </span>
            <ul className="mt-1.5 flex flex-col gap-0.5">
              {unavailable.map((u) => (
                <li key={u.resource}>
                  <b className="font-semibold text-gray-900">{u.label}</b> — {u.consequence}
                </li>
              ))}
            </ul>
            <span className="mt-1.5 block">
              Give the token the permissions and gather again to include them, or carry on — the
              summary lists them.{' '}
              <Link to="/permissions" className="font-medium text-purple-500 hover:underline">
                See Source Token Permissions
              </Link>
            </span>
          </Callout>
        )}

        {completed && summary && (
          <Callout tone="success">
            {summary.project_count} projects, {summary.config_count} configs,{' '}
            {summary.secret_count.toLocaleString()} secrets and {summary.group_count} groups.
            {emptyCount > 0 && (
              <>
                {' '}
                {emptyCount} resource {emptyCount === 1 ? 'type was' : 'types were'} empty and{' '}
                {reportOnlyCount} are report-only. All of them are listed above.
              </>
            )}
          </Callout>
        )}

        <FooterBar
          left={
            <Button variant="ghost" onClick={() => navigate(inFlight ? '/standby' : '/preflight')} disabled={gathering}>
              Back
            </Button>
          }
          right={
            <Button size="md" disabled={!completed} onClick={() => navigate(next ?? (inFlight ? '/compare' : '/scope'))}>
              Continue <ArrowRight size={14} />
            </Button>
          }
        />
      </div>
    </div>
  );
}
