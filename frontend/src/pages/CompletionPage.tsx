import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowRight, Download, Loader2, MinusCircle, RotateCw } from 'lucide-react';
import {
  Block,
  BlockHeader,
  BlockRow,
  Button,
  Callout,
  Eyebrow,
  FooterBar,
  PageHeading,
  Pill,
  StatusIcon,
} from '@/components/ui/primitives';
import { errorsExportURL, finalReportURL, getErrors, getSummary, setScope, setStandby } from '@/api/client';
import { NextStep, type NextStepChoice } from '@/components/standby/NextStep';
import { formatWhen } from '@/lib/when';
import { ScopeBand, type ScopeBandData } from '@/components/scope/ScopeBand';
import type { SummaryResponse } from '@/api/client';
import type { ErrorsResponse } from '@/types/errors';

/**
 * The summary — the record you keep.
 *
 * The headline is set by whether anything is still open, not by whether the run
 * reached the end. A migration that ran every phase and left 49 failures is not
 * a success with a footnote; it is unfinished, and saying so is the difference
 * between someone fixing it today and someone finding out in a month.
 */
export default function CompletionPage() {
  const navigate = useNavigate();
  const [data, setData] = useState<SummaryResponse | null>(null);
  const [errors, setErrors] = useState<ErrorsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [choice, setChoice] = useState<NextStepChoice>('wait');
  const [choiceError, setChoiceError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    try {
      const [summary, errs] = await Promise.all([getSummary(), getErrors().catch(() => null)]);
      setData(summary);
      setErrors(errs);
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Could not load the summary');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  function downloadRecord() {
    if (!data) return;
    const record = {
      generated_at: new Date().toISOString(),
      summary: data.summary,
      checkpoint: data.checkpoint,
      manual_step_count: data.manual_step_count,
      // The resolutions are the half of the record worth keeping: not only what
      // failed, but what was done about it.
      error_classes: errors?.classes ?? [],
    };
    const blob = new Blob([JSON.stringify(record, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'migration-record.json';
    a.click();
    URL.revokeObjectURL(url);
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center gap-2.5 py-20 text-[13px] text-gray-500">
        <Loader2 size={16} className="animate-spin-slow text-purple-500" />
        Loading migration summary…
      </div>
    );
  }

  if (loadError) {
    return (
      <div className="mx-auto w-full max-w-[720px] px-6 py-8">
        <Callout tone="error">{loadError}</Callout>
        <div className="mt-4">
          <Button variant="secondary" onClick={load}>
            Try Again
          </Button>
        </div>
      </div>
    );
  }

  const summary = data?.summary;
  const openErrors = errors?.open ?? 0;
  const resolvedClasses = errors?.classes.filter((c) => c.open_count === 0) ?? [];
  const openClasses = errors?.classes.filter((c) => c.open_count > 0) ?? [];
  const manualSteps = data?.manual_step_count ?? 0;
  const tokenTotal = data?.token_counts?.total ?? 0;
  // Set aside for later is not done: cutover waits on those too.
  const tokensPending = (data?.token_counts?.pending ?? 0) + (data?.token_counts?.deferred ?? 0);
  const outstanding = manualSteps + tokensPending;

  // What the last run covered. A partial's summary is a checkpoint rather than
  // an ending: its main action is the full run, and cutover waits for one.
  const run = data?.run ?? null;
  const partial = run?.mode === 'partial';
  const source = data?.source_summary;
  const cutoverBlocked = Boolean(data?.cutover_blocked);
  const earlier = run?.previously_migrated ?? [];
  const inScope = run?.resolution.projects.length ?? 0;
  const totalProjects = run?.resolution.total_projects ?? summary?.project_count ?? 0;

  // Between the full run and cutover. The run just finished is a top-up when
  // the history says so; the choice of what next is offered until cutover.
  const standby = data?.standby;
  const lastRun = standby?.last_run;
  const isTopUp = !partial && lastRun?.kind === 'top_up';
  const cutOver = Boolean(standby?.cutover_at);
  const decide = !partial && Boolean(standby?.full_done) && !cutOver;
  // Runs are newest first: the one before the top-up is what it was measured against.
  const topUpSince = isTopUp ? (standby?.runs[1]?.at ?? null) : null;

  const band: ScopeBandData | null = isTopUp
    ? {
        // From the history alone, so it still reads after a restart.
        mode: 'full',
        projects: [],
        total: totalProjects,
        previouslyMigrated: 0,
        topUp: { number: lastRun?.number ?? 1, since: topUpSince },
      }
    : run
    ? {
        mode: run.mode,
        projects: run.resolution.projects,
        total: totalProjects,
        previouslyMigrated: earlier.length,
        previouslyMigratedAt: run.previously_migrated_at,
      }
    : null;

  async function continueFromChoice() {
    setStarting(true);
    setChoiceError(null);
    try {
      await setStandby(choice);
      navigate(choice === 'wait' ? '/standby' : '/cutover');
    } catch (err) {
      setChoiceError(err instanceof Error ? err.message : 'Could not record the choice');
      setStarting(false);
    }
  }

  const title = isTopUp
    ? openErrors > 0
      ? 'Top-Up Finished With Work Outstanding'
      : 'Top-Up Complete'
    : partial
    ? openErrors > 0
      ? 'Partial Migration Finished With Work Outstanding'
      : 'Partial Migration Complete'
    : openErrors > 0
      ? 'Migration Finished With Work Outstanding'
      : 'Migration Complete';

  const subtitle = cutOver
    ? `Cut over ${formatWhen(standby?.cutover_at)}. The migration is finished.`
    : isTopUp && openErrors === 0
      ? `The target matches the source as of ${formatWhen(lastRun?.at)}.`
      : partial
    ? `${inScope} ${inScope === 1 ? 'project is' : 'projects are'} on the target. The other ${
        totalProjects - inScope
      } ${totalProjects - inScope === 1 ? 'is' : 'are'} still only on the source.`
    : openErrors > 0
      ? `Every phase ran, but ${openErrors} ${openErrors === 1 ? 'failure is' : 'failures are'} still open.`
      : 'Every phase ran and nothing is outstanding.';

  async function runFull() {
    setStarting(true);
    try {
      // Preselect full, so the Scope page opens on "finish the job".
      await setScope({ mode: 'full' });
    } catch {
      // Scope will still open; the choice can be made there.
    } finally {
      setStarting(false);
    }
    navigate('/scope');
  }

  const topUp = run?.top_up;
  const topUpChanges = topUp ? topUp.created + topUp.updated + topUp.deleted : 0;

  return (
    <div className="mx-auto w-full max-w-[720px] px-6 py-6">
      <PageHeading title={title} subtitle={subtitle} />

      <div className="mt-5 flex flex-col gap-3.5">
        <ScopeBand scope={band} />

        {partial && openErrors === 0 && (
          <Callout tone="success">
            <span className="font-semibold text-gray-900">Nothing failed.</span> Try the target with
            these projects — point a staging service at it, read a few secrets back. When you are
            ready, the full run migrates the rest and brings these up to date with anything that
            changed on the source in the meantime.
          </Callout>
        )}

        {openErrors > 0 ? (
          <Callout tone="error">
            <span className="font-semibold text-gray-900">
              {openErrors} {openErrors === 1 ? 'error is' : 'errors are'} still open
            </span>{' '}
            across {openClasses.length} {openClasses.length === 1 ? 'cause' : 'causes'}. They are
            fixable from triage, and the export records them as open.
          </Callout>
        ) : partial ? null : (
          <Callout tone="success">
            {errors && errors.total > 0
              ? `All ${errors.total} recorded failures were resolved.`
              : 'Nothing failed during the run.'}{' '}
            {outstanding > 0 &&
              `${outstanding} ${outstanding === 1 ? 'thing remains' : 'things remain'} to do by hand — see below.`}
          </Callout>
        )}

        {isTopUp && (
          <div className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-gray-75 bg-gray-75 sm:grid-cols-4">
            <Tile label="Created" value={lastRun?.created ?? 0} />
            <Tile label="Updated" value={lastRun?.updated ?? 0} />
            <Tile label="Failed" value={lastRun?.failed ?? 0} tone={(lastRun?.failed ?? 0) > 0 ? 'bad' : undefined} />
            <Tile label="Already current" value={topUp?.current ?? 0} />
          </div>
        )}

        {summary && !isTopUp && (
          <div className="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-gray-75 bg-gray-75 sm:grid-cols-4">
            <Tile label="Projects" value={summary.project_count} of={partial ? source?.project_count : undefined} />
            <Tile label="Configs" value={summary.config_count} of={partial ? source?.config_count : undefined} />
            <Tile label="Secrets" value={summary.secret_count} of={partial ? source?.secret_count : undefined} />
            <Tile label="Errors open" value={openErrors} tone={openErrors > 0 ? 'bad' : undefined} />
          </div>
        )}

        {partial && run && (
          <Block>
            <BlockHeader title="Not Migrated Yet">
              <Pill tone="skipped">Waits for the full run</Pill>
            </BlockHeader>
            <BlockRow>
              <MinusCircle size={13} className="shrink-0 text-gray-400" />
              <span>
                {run.resolution.deferred_projects} {run.resolution.deferred_projects === 1 ? 'project' : 'projects'}
              </span>
            </BlockRow>
            {(run.resolution.defers_workplace_settings || run.resolution.deferred_policies > 0) && (
              <BlockRow>
                <MinusCircle size={13} className="shrink-0 text-gray-400" />
                <span>
                  {[
                    run.resolution.defers_workplace_settings && 'Workplace settings',
                    run.resolution.deferred_policies > 0 &&
                      `${run.resolution.deferred_policies} change-request ${
                        run.resolution.deferred_policies === 1 ? 'policy' : 'policies'
                      }`,
                  ]
                    .filter(Boolean)
                    .join(' and ')}
                </span>
              </BlockRow>
            )}
            {run.resolution.deferred_groups + run.resolution.deferred_roles + run.resolution.deferred_service_accounts >
              0 && (
              <BlockRow>
                <MinusCircle size={13} className="shrink-0 text-gray-400" />
                <span>
                  {run.resolution.deferred_groups} groups, {run.resolution.deferred_roles} roles and{' '}
                  {run.resolution.deferred_service_accounts} service accounts
                </span>
              </BlockRow>
            )}
          </Block>
        )}

        {!partial && !isTopUp && earlier.length > 0 && (
          <Block>
            <BlockHeader title="This Run" />
            <BlockRow>
              <StatusIcon status="completed" />
              <span className="min-w-0 flex-1">
                Migrated {run!.remaining} {run!.remaining === 1 ? 'project' : 'projects'} and the rest of
                the workplace layer
              </span>
            </BlockRow>
            <BlockRow>
              {run!.top_up_error ? (
                <StatusIcon status="failed" />
              ) : (
                <RotateCw size={14} className="shrink-0 text-purple-500" />
              )}
              <span className="min-w-0 flex-1">
                {run!.top_up_error
                  ? `Could not bring the ${earlier.length} earlier ${earlier.length === 1 ? 'project' : 'projects'} up to date`
                  : `Brought the ${earlier.length} ${earlier.length === 1 ? 'project' : 'projects'} from the ${
                      run!.previously_migrated_at ? `${formatDate(run!.previously_migrated_at)} ` : ''
                    }partial up to date`}
                <span className="block text-xs text-gray-500">
                  {run!.top_up_error
                    ? `${run!.top_up_error}. Compare again to retry — only what differs is written.`
                    : topUpChanges === 0
                      ? 'They already matched the source.'
                      : `${topUpChanges} ${topUpChanges === 1 ? 'change' : 'changes'}`}
                </span>
              </span>
            </BlockRow>
          </Block>
        )}

        {openClasses.length > 0 && (
          <Block>
            <BlockHeader title={`Open Errors · ${openClasses.length} ${openClasses.length === 1 ? 'Class' : 'Classes'}`}>
              <Link
                to="/migrate/errors"
                className="text-[12.5px] font-medium text-purple-500 hover:underline"
              >
                Open triage
              </Link>
            </BlockHeader>
            {openClasses.map((cls) => (
              <BlockRow key={cls.key}>
                <StatusIcon status={cls.severity === 'warning' ? 'warning' : 'failed'} />
                <span className="min-w-0 flex-1 truncate">{cls.title}</span>
                <span className="shrink-0 text-gray-500">
                  {cls.open_count} {cls.resource_label.toLowerCase()}
                </span>
              </BlockRow>
            ))}
          </Block>
        )}

        {resolvedClasses.length > 0 && (
          <Block>
            <BlockHeader title={`Errors Resolved · ${resolvedClasses.length} ${resolvedClasses.length === 1 ? 'Class' : 'Classes'}`} />
            {resolvedClasses.map((cls) => (
              <BlockRow key={cls.key}>
                <StatusIcon status="completed" />
                <span className="min-w-0 flex-1 truncate">{cls.title}</span>
                <span className="shrink-0 text-gray-500">
                  {cls.count} · {cls.resolution}
                </span>
              </BlockRow>
            ))}
          </Block>
        )}

        {(tokenTotal > 0 || manualSteps > 0) && (
          <HandWork
            tokenTotal={tokenTotal}
            tokensPending={tokensPending}
            manualKinds={data?.manual_steps ?? []}
            manualSteps={manualSteps}
            onTokens={() => navigate('/service-tokens')}
            onChecklist={() => navigate('/manual-steps')}
          />
        )}

        {data?.checkpoint?.error && (
          <div>
            <Eyebrow className="mb-1.5">Checkpoint</Eyebrow>
            <Callout tone="warning">{data.checkpoint.error}</Callout>
          </div>
        )}

        {decide && (
          <NextStep choice={choice} onChoose={setChoice} afterTopUp={isTopUp} />
        )}
        {choiceError && <Callout tone="error">{choiceError}</Callout>}

        {/* One row, one filled button. The record is something you keep, not the
            next step, so it sits on the left in gray; on the right is where to
            go next, with the step that follows filled — the full run after a
            partial, cutover after a full run — and everything the same size. */}
        <FooterBar
          left={
            <>
              <Button variant="secondary" size="md" onClick={downloadRecord}>
                <Download size={14} /> Export Record
              </Button>
              {errors && errors.total > 0 && (
                <Button
                  variant="ghost"
                  size="md"
                  onClick={() => {
                    window.location.href = errorsExportURL;
                  }}
                >
                  <Download size={14} /> Errors As CSV
                </Button>
              )}
            </>
          }
          right={
            <>
              {openErrors > 0 && (
                <Button variant="secondary" size="md" onClick={() => navigate('/migrate/errors')}>
                  Open Triage
                </Button>
              )}
              {partial ? (
                <>
                  <Button
                    variant="secondary"
                    size="md"
                    disabled
                    title="Cutover becomes available after a full migration."
                  >
                    Cut Over
                  </Button>
                  <Button size="md" onClick={runFull} disabled={starting}>
                    Run Full Migration <ArrowRight size={14} />
                  </Button>
                </>
              ) : decide ? (
                <Button size="md" onClick={continueFromChoice} disabled={starting}>
                  Continue <ArrowRight size={14} />
                </Button>
              ) : cutOver ? (
                <Button
                  variant="secondary"
                  size="md"
                  onClick={() => {
                    window.location.href = finalReportURL;
                  }}
                >
                  <Download size={14} /> Download Report
                </Button>
              ) : (
                // A dry run records nothing, so there is no choice to offer; the
                // cutover plan can still be looked at.
                <Button variant="danger" size="md" onClick={() => navigate('/cutover')} disabled={cutoverBlocked}>
                  Cut Over <ArrowRight size={14} />
                </Button>
              )}
            </>
          }
        />
        {cutoverBlocked && (
          <p className="-mt-1.5 text-right text-xs text-gray-500">
            Cutover becomes available after a full migration.
          </p>
        )}
      </div>
    </div>
  );
}

function Tile({ label, value, tone, of }: { label: string; value: number; tone?: 'bad'; of?: number }) {
  return (
    <div className="bg-gray-0 px-4 py-3.5">
      <div className="text-[10.5px] font-semibold uppercase tracking-[0.05em] text-gray-400">
        {label}
      </div>
      <b
        className={`block text-2xl font-semibold tabular-nums leading-tight ${
          tone === 'bad' ? 'text-red-600' : 'text-gray-900'
        }`}
      >
        {value.toLocaleString()}
        {of !== undefined && (
          <small className="ml-1 text-[13px] font-medium text-gray-500">of {of.toLocaleString()}</small>
        )}
      </b>
    </div>
  );
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

/** How each kind of manual step reads when counted. */
const MANUAL_KINDS: Record<string, [string, string]> = {
  integration: ['integration', 'integrations'],
  sync: ['secrets sync', 'secrets syncs'],
  rotated_secret: ['rotated secret', 'rotated secrets'],
  webhook_secret: ['webhook signing secret', 'webhook signing secrets'],
  personal_config: ['personal config', 'personal configs'],
};

/**
 * The work the migration cannot do for you, as two tasks with their own state.
 *
 * It used to be one total — tokens and manual steps added together — above two
 * sentences, which said how much was left but not what, or what doing it meant.
 * Each row now names the task, says where it stands, and has one way in.
 */
function HandWork({
  tokenTotal,
  tokensPending,
  manualKinds,
  manualSteps,
  onTokens,
  onChecklist,
}: {
  tokenTotal: number;
  tokensPending: number;
  manualKinds: { category: string; count: number }[];
  manualSteps: number;
  onTokens: () => void;
  onChecklist: () => void;
}) {
  const breakdown = manualKinds
    .map(({ category, count }) => {
      const [one, many] = MANUAL_KINDS[category] ?? [category, category];
      return `${count} ${count === 1 ? one : many}`;
    })
    .join(' · ');

  return (
    <Block>
      <BlockHeader title="Still To Do By Hand" />
      <p className="border-b border-gray-75 px-3.5 py-2.5 text-[12.5px] text-gray-500">
        The API cannot carry these across, so they are finished on the target by hand.
      </p>

      {tokenTotal > 0 && (
        <HandWorkRow
          done={tokensPending === 0}
          title="Service Tokens"
          status={
            tokensPending > 0
              ? `${tokensPending} of ${tokenTotal} undecided`
              : `All ${tokenTotal} handled`
          }
          detail={
            tokensPending > 0
              ? 'Issue each on the target, or skip it onto a worklist to recreate yourself. Cutover waits until every one is decided.'
              : 'Each one was issued on the target or put on the worklist.'
          }
          action={tokensPending > 0 ? 'Issue Or Skip' : 'Review'}
          onAction={onTokens}
        />
      )}

      {manualSteps > 0 && (
        <HandWorkRow
          done={false}
          title="Recreate On The Target"
          status={`${manualSteps} ${manualSteps === 1 ? 'item' : 'items'}`}
          detail={breakdown || 'Resources the API cannot read back.'}
          action="Open Checklist"
          onAction={onChecklist}
        />
      )}
    </Block>
  );
}

function HandWorkRow({
  done,
  title,
  status,
  detail,
  action,
  onAction,
}: {
  done: boolean;
  title: string;
  status: string;
  detail: string;
  action: string;
  onAction: () => void;
}) {
  return (
    <div className="flex items-center gap-3 border-b border-gray-75 px-3.5 py-3 last:border-b-0">
      <StatusIcon status={done ? 'completed' : 'warning'} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <b className="text-[13px] font-semibold text-gray-900">{title}</b>
          <Pill tone={done ? 'done' : 'warning'}>{status}</Pill>
        </div>
        <p className="mt-0.5 text-[12.5px] text-gray-600">{detail}</p>
      </div>
      <Button variant="secondary" onClick={onAction}>
        {action}
      </Button>
    </div>
  );
}
