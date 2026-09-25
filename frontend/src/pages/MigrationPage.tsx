import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { AlertTriangle, ArrowRight, Pause, Play, RotateCw } from 'lucide-react';
import {
  Block,
  BlockHeader,
  BlockRow,
  Button,
  Callout,
  FooterBar,
  Pill,
  ProgressBar,
  StatusIcon,
} from '@/components/ui/primitives';
import PhaseTimeline, { type PhaseState, type TimelinePhase } from '@/components/errors/PhaseTimeline';
import ErrorClassDetail from '@/components/errors/ErrorClassDetail';
import {
  getErrorClass,
  getErrors,
  getMigrationStatus,
  pauseMigration,
  resolveErrorClass,
  resumeMigration,
  startMigration,
  subscribeSSE,
} from '@/api/client';
import type { MigrateProgress } from '@/api/client';
import { ScopeBand, useScopeBand } from '@/components/scope/ScopeBand';
import type { ErrorRecord, ErrorsResponse, ResolveRequest } from '@/types/errors';
import { useWizard } from '@/hooks/useWizard';

/** The ten phases, in the order the executor runs them. */
const PHASES: { key: string; label: string }[] = [
  { key: 'workplace_settings_and_roles', label: 'Workplace & Roles' },
  { key: 'groups_and_service_accounts', label: 'Groups & Service Accounts' },
  { key: 'projects_and_environments', label: 'Projects & Environments' },
  { key: 'configs', label: 'Configs' },
  { key: 'secrets_and_locks', label: 'Secrets & Config Locks' },
  { key: 'config_inheritance', label: 'Config Inheritance' },
  { key: 'webhooks', label: 'Webhooks' },
  { key: 'members', label: 'Members & User Access' },
  { key: 'change_request_policies', label: 'Change Request Policies' },
  { key: 'trusted_ips', label: 'Trusted IPs' },
];

interface PhaseProgress {
  status: 'pending' | 'started' | 'completed' | 'completed_with_errors' | 'skipped' | 'failed';
  current: number;
  total: number;
  lastItem: string;
  /** Why a skipped phase had nothing to do. */
  reason?: string;
}

/** How far along a status is, so a late report can never move a phase back. */
function statusRank(status: PhaseProgress['status'] | undefined): number {
  if (!status || status === 'pending') return 0;
  return status === 'started' ? 1 : 2;
}

/**
 * Folds the server's record of each phase into what the events said.
 *
 * The event stream can connect after a phase with nothing to do has started
 * and finished, and a refresh mid-run starts with no events at all. The
 * server's record fills those in; whichever is further along wins.
 */
export function mergePhases(
  prev: Record<string, PhaseProgress>,
  server: Record<string, { status: string; reason?: string }> | undefined,
): Record<string, PhaseProgress> {
  if (!server) return prev;
  let next = prev;
  for (const [name, st] of Object.entries(server)) {
    const status = st.status as PhaseProgress['status'];
    const existing = prev[name];
    if (statusRank(status) <= statusRank(existing?.status)) continue;
    if (next === prev) next = { ...prev };
    next[name] = {
      ...(existing ?? { current: 0, total: 0, lastItem: '' }),
      status,
      reason: st.reason,
    };
  }
  return next;
}

type RunState = 'idle' | 'running' | 'paused' | 'completed' | 'failed';

/**
 * The migration screen.
 *
 * It splits, and it is the only screen that does. On the left the phases, with
 * error classes nested underneath the phase that produced them. On the right
 * whatever you have selected: the running phase by default, or a class of
 * failures with its cause, its evidence and the one action that clears all of it.
 */
export default function MigrationPage() {
  const navigate = useNavigate();
  const { setBlockingError } = useWizard();

  const [phases, setPhases] = useState<Record<string, PhaseProgress>>({});
  const [runState, setRunState] = useState<RunState>('idle');
  const [runError, setRunError] = useState<string | null>(null);
  const [errors, setErrors] = useState<ErrorsResponse | null>(null);
  const [selectedClass, setSelectedClass] = useState<string | null>(null);
  const [classRecords, setClassRecords] = useState<ErrorRecord[]>([]);
  const [classRecordTotal, setClassRecordTotal] = useState(0);
  const [busyFix, setBusyFix] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const scope = useScopeBand();
  // A full run after a partial has a second stage: the top-up of what the
  // partial moved. Its progress arrives as converge events, counted here.
  const [stage, setStage] = useState<'remaining' | 'previously_migrated' | 'done'>('remaining');
  // How the run actually split its projects, from the server. The scope cannot
  // say: an earlier project deleted from the target since moves to stage one.
  const [split, setSplit] = useState<{ previously: number; reintroduced: string[] } | null>(null);
  const applySplit = useCallback((run: { previously_migrated?: string[] | null; reintroduced?: string[] | null } | null | undefined) => {
    if (!run) return;
    setSplit({ previously: run.previously_migrated?.length ?? 0, reintroduced: run.reintroduced ?? [] });
  }, []);
  const [topUp, setTopUp] = useState({ applied: 0, failed: 0, lastItem: '' });

  const startedRef = useRef(false);
  const unsubRef = useRef<(() => void) | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const refreshErrors = useCallback(async () => {
    try {
      setErrors(await getErrors());
    } catch {
      // Triage is supplementary. A failure to load it must not take down the
      // progress view, which is the thing someone is actually watching.
    }
  }, []);

  const stopPolling = useCallback(() => {
    if (pollRef.current !== null) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  const cleanupSSE = useCallback(() => {
    unsubRef.current?.();
    unsubRef.current = null;
  }, []);

  const finish = useCallback(
    (status: string, message?: string) => {
      setRunState(status === 'completed' ? 'completed' : 'failed');
      if (status !== 'completed') setRunError(message || 'The migration stopped');
      stopPolling();
      void refreshErrors();
    },
    [stopPolling, refreshErrors],
  );

  const onEvent = useCallback(
    (type: string, data: unknown) => {
      if (type === 'migrate_progress') {
        const p = data as MigrateProgress;
        setPhases((prev) => {
          const existing = prev[p.name] ?? { status: 'pending', current: 0, total: 0, lastItem: '' };
          if (!p.resource) {
            return {
              ...prev,
              [p.name]: { ...existing, status: p.status as PhaseProgress['status'], reason: p.reason },
            };
          }
          return {
            ...prev,
            [p.name]: {
              status: 'started',
              current: p.current,
              total: p.total,
              lastItem: p.item,
            },
          };
        });
        // A failure arrives with the class it was filed under, so the timeline
        // can nest it without re-fetching the whole error list on every item.
        if (p.status === 'failed') void refreshErrors();
      } else if (type === 'phase_complete') {
        const p = data as MigrateProgress;
        setPhases((prev) => ({
          ...prev,
          [p.name]: { ...(prev[p.name] ?? { current: 0, total: 0, lastItem: '' }), status: 'completed' },
        }));
      } else if (type === 'migration_stage') {
        const d = data as { stage?: string; previously_migrated?: number; reintroduced?: string[] | null };
        const st = d.stage;
        if (st === 'remaining' || st === 'previously_migrated' || st === 'done') setStage(st);
        if (typeof d.previously_migrated === 'number') {
          setSplit({ previously: d.previously_migrated, reintroduced: d.reintroduced ?? [] });
        }
      } else if (type === 'converge_progress') {
        const p = data as MigrateProgress;
        if (p.status === 'completed' || p.status === 'failed') {
          setTopUp((prev) => ({
            applied: prev.applied + (p.status === 'completed' ? 1 : 0),
            failed: prev.failed + (p.status === 'failed' ? 1 : 0),
            lastItem: p.item || prev.lastItem,
          }));
        }
        if (p.status === 'failed') void refreshErrors();
      } else if (type === 'migration_complete') {
        const result = data as { status: string; error?: string };
        finish(result.status, result.error);
      } else if (type === 'errors_changed' || type === 'rerun_complete') {
        void refreshErrors();
        if (type === 'rerun_complete') {
          const r = data as { resolved?: number; still_open?: number };
          setNotice(
            `Re-run finished — ${r.resolved ?? 0} cleared, ${r.still_open ?? 0} still open.`,
          );
          setBusyFix(false);
        }
      }
    },
    [finish, refreshErrors],
  );

  const begin = useCallback(async () => {
    setRunState('running');
    setRunError(null);
    setNotice(null);

    const sse = subscribeSSE(onEvent);
    unsubRef.current = sse.unsubscribe;

    try {
      await startMigration();
      pollRef.current = setInterval(async () => {
        try {
          const status = await getMigrationStatus();
          setPhases((prev) => mergePhases(prev, status.phases));
          applySplit(status.run);
          if (!status.migrating && status.migration_result) {
            finish(status.migration_result, status.error);
          }
        } catch {
          // A dropped poll is not worth surfacing; SSE is the primary channel.
        }
      }, 3000);
    } catch (err) {
      setRunState('failed');
      setRunError(err instanceof Error ? err.message : 'Could not start the migration');
      cleanupSSE();
    }
  }, [onEvent, finish, cleanupSSE, applySplit]);

  // Start once. A refresh mid-run must attach to the run in progress rather than
  // trying to start a second one against the same target.
  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;

    void (async () => {
      try {
        const status = await getMigrationStatus();
        setPhases((prev) => mergePhases(prev, status.phases));
        applySplit(status.run);
        if (status.migrating) {
          setRunState(status.paused ? 'paused' : 'running');
          const sse = subscribeSSE(onEvent);
          unsubRef.current = sse.unsubscribe;
          await refreshErrors();
          return;
        }
        if (status.migration_result) {
          finish(status.migration_result, status.error);
          const sse = subscribeSSE(onEvent);
          unsubRef.current = sse.unsubscribe;
          return;
        }
      } catch {
        // No status to attach to — start fresh below.
      }
      await begin();
    })();

    return () => {
      stopPolling();
      cleanupSSE();
    };
  }, [begin, onEvent, finish, refreshErrors, stopPolling, cleanupSSE, applySplit]);

  // Load the instances behind whichever class is selected.
  useEffect(() => {
    if (!selectedClass) {
      setClassRecords([]);
      setClassRecordTotal(0);
      return;
    }
    void (async () => {
      try {
        const resp = await getErrorClass(selectedClass);
        setClassRecords(resp.records);
        setClassRecordTotal(resp.total);
      } catch {
        setClassRecords([]);
      }
    })();
  }, [selectedClass, errors]);

  const openErrors = errors?.open ?? 0;
  useEffect(() => {
    setBlockingError(runState === 'failed');
  }, [runState, setBlockingError]);

  // Two stages only when an earlier run moved something in this scope. A first
  // run looks exactly as it always has.
  const earlierCount = split?.previously ?? scope?.previouslyMigrated ?? 0;
  const twoStages = Boolean(scope && (scope.previouslyMigrated > 0 || earlierCount > 0));
  const topUpRun = Boolean(scope?.topUp && scope.mode === 'full');
  const reintroduced = split?.reintroduced ?? [];

  const timeline = useMemo<TimelinePhase[]>(
    () => buildTimeline(phases, errors, runState),
    [phases, errors, runState],
  );

  const activePhase = useMemo(
    () => PHASES.find((p) => phases[p.key]?.status === 'started'),
    [phases],
  );

  const selected = errors?.classes.find((c) => c.key === selectedClass) ?? null;

  async function applyFix(req: ResolveRequest) {
    if (!selected) return;
    setBusyFix(true);
    setNotice(null);
    try {
      const resp = await resolveErrorClass(selected.key, req);
      setNotice(
        resp.status === 'resolved'
          ? `${resp.resolution}. ${resp.resolved ?? 0} cleared.`
          : `Re-running ${resp.item_count} ${resp.item_count === 1 ? 'item' : 'items'}.`,
      );
      if (resp.status === 'resolved') setBusyFix(false);
      await refreshErrors();
    } catch (err) {
      setNotice(err instanceof Error ? err.message : 'The fix could not be applied');
      setBusyFix(false);
    }
  }

  async function togglePause() {
    try {
      if (runState === 'paused') {
        await resumeMigration();
        setRunState('running');
      } else {
        await pauseMigration();
        setRunState('paused');
      }
    } catch (err) {
      setNotice(err instanceof Error ? err.message : 'Could not change the run state');
    }
  }

  const done = runState === 'completed' || runState === 'failed';

  return (
    <div className="flex min-h-[560px] flex-col">
      {/* Run header */}
      <div className="flex flex-wrap items-center gap-2.5 border-b border-gray-75 bg-gray-0 px-6 py-3">
        <span className="text-[13.5px] font-semibold text-gray-900">Migrating</span>
        {runState === 'running' && stage !== 'previously_migrated' && (
          <Pill tone="running">
            <StatusIcon status="running" size={12} /> Phase {currentPhaseNumber(phases)} of {PHASES.length}
          </Pill>
        )}
        {/* Every phase has finished by now; "phase 10 of 10" would read as a hang. */}
        {runState === 'running' && stage === 'previously_migrated' && (
          <Pill tone="running">
            <StatusIcon status="running" size={12} /> Updating earlier projects
          </Pill>
        )}
        {runState === 'paused' && (
          <Pill tone="warning">
            <Pause size={12} /> Paused
          </Pill>
        )}
        {runState === 'completed' && <Pill tone="done">All phases run</Pill>}
        {runState === 'failed' && <Pill tone="error">Stopped</Pill>}

        {openErrors > 0 && (
          <Pill tone="error">
            <AlertTriangle size={12} /> {openErrors} {openErrors === 1 ? 'error' : 'errors'} in{' '}
            {errors?.class_count} {errors?.class_count === 1 ? 'class' : 'classes'}
          </Pill>
        )}

        <span className="ml-auto flex items-center gap-2">
          {openErrors > 0 && (
            <Link
              to="/migrate/errors"
              className="inline-flex h-8 items-center gap-2 rounded-md px-3 text-[13px] font-semibold text-gray-600 hover:bg-gray-50 hover:text-gray-900"
            >
              Open Triage <ArrowRight size={13} />
            </Link>
          )}
          {!done && (
            <Button variant="secondary" onClick={togglePause}>
              {runState === 'paused' ? <Play size={13} /> : <Pause size={13} />}
              {runState === 'paused' ? 'Resume' : 'Pause'}
            </Button>
          )}
          {done && (
            <Button onClick={() => navigate('/complete')}>
              Continue To Summary <ArrowRight size={13} />
            </Button>
          )}
        </span>
      </div>

      {scope && (scope.mode === 'partial' || scope.previouslyMigrated > 0) && (
        <div className="border-b border-gray-75 bg-gray-0 px-6 py-2.5">
          <ScopeBand scope={scope} />
        </div>
      )}

      {reintroduced.length > 0 && (
        <div className="border-b border-gray-75 bg-gray-0 px-6 py-2.5">
          <Callout tone="info">
            <span className="font-mono">{reintroduced.join(', ')}</span>{' '}
            {reintroduced.length === 1 ? 'was' : 'were'} migrated before but{' '}
            {reintroduced.length === 1 ? 'is' : 'are'} no longer on the target, so{' '}
            {reintroduced.length === 1 ? 'it is' : 'they are'} migrated again in full.
          </Callout>
        </div>
      )}

      {notice && (
        <div className="border-b border-gray-75 bg-gray-0 px-6 py-2.5">
          <Callout tone="info">{notice}</Callout>
        </div>
      )}
      {runError && (
        <div className="border-b border-gray-75 bg-gray-0 px-6 py-2.5">
          <Callout tone="error">{runError}</Callout>
        </div>
      )}

      {/* Split: phases left, detail right */}
      <div className="flex flex-1 flex-col lg:flex-row">
        <aside className="w-full shrink-0 border-b border-gray-75 bg-gray-0 px-5 py-4 lg:w-[310px] lg:border-b-0 lg:border-r">
          {/* In a top-up, stage one is the workplace layer and any project not on
              the target — every other project is brought up to date below. */}
          {twoStages && (
            <StageHeading done={stage !== 'remaining'}>
              {topUpRun ? 'Not Yet On The Target' : 'Remaining Resources'}
            </StageHeading>
          )}
          <PhaseTimeline
            phases={timeline}
            selectedClass={selectedClass}
            onSelectClass={(key) => setSelectedClass(key === selectedClass ? null : key)}
          />
          {twoStages && (
            <PreviouslyMigrated
              stage={stage}
              projects={earlierCount}
              applied={topUp.applied}
              failed={topUp.failed}
              lastItem={topUp.lastItem}
            />
          )}
        </aside>

        <div className="min-w-0 flex-1 px-6 py-5">
          {selected ? (
            <ErrorClassDetail
              cls={selected}
              records={classRecords}
              totalRecords={classRecordTotal}
              busy={busyFix}
              onApplyFix={applyFix}
              onDismiss={() => setSelectedClass(null)}
            />
          ) : (
            <RunningPhaseDetail
              phase={activePhase}
              progress={activePhase ? phases[activePhase.key] : undefined}
              runState={runState}
              errors={errors}
            />
          )}
        </div>
      </div>

      <div className="border-t border-gray-75 bg-gray-0 px-6 py-3">
        <FooterBar
          left={
            <span className="text-[12.5px] text-gray-500">
              {done
                ? 'Nothing is rolled back. Anything still open is carried into the summary.'
                : 'You can pause at any point. Work already written to the target is kept.'}
            </span>
          }
          right={
            done ? (
              <Button onClick={() => navigate('/complete')}>
                Continue <ArrowRight size={13} />
              </Button>
            ) : (
              <Button variant="secondary" disabled>
                Continue <ArrowRight size={13} />
              </Button>
            )
          }
        />
      </div>
    </div>
  );
}

/** The default right-hand pane: whatever phase is running now. */
function RunningPhaseDetail({
  phase,
  progress,
  runState,
  errors,
}: {
  phase?: { key: string; label: string };
  progress?: PhaseProgress;
  runState: RunState;
  errors: ErrorsResponse | null;
}) {
  if (runState === 'completed' || runState === 'failed') {
    const open = errors?.open ?? 0;
    return (
      <div className="flex flex-col gap-3.5">
        <h2 className="text-[18px] font-semibold text-gray-900">
          {open > 0 ? 'Finished with work outstanding' : 'Migration complete'}
        </h2>
        {open > 0 ? (
          <Callout tone="warning">
            All phases ran, but {open} {open === 1 ? 'failure is' : 'failures are'} still open.
            Select a class on the left to see what happened and how to clear it.
          </Callout>
        ) : (
          <Callout tone="success">
            Every phase ran and nothing is outstanding. The summary lists what still has to be done
            by hand.
          </Callout>
        )}
      </div>
    );
  }

  if (!phase) {
    return (
      <div className="flex flex-col gap-3.5">
        <h2 className="text-[18px] font-semibold text-gray-900">Starting</h2>
        <p className="text-[13px] text-gray-500">
          Waiting for the first phase to report. Nothing has been written yet.
        </p>
      </div>
    );
  }

  const pct = progress?.total ? (progress.current / progress.total) * 100 : 0;

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="text-[18px] font-semibold text-gray-900">{phase.label}</h2>
          <p className="mt-0.5 text-[12.5px] text-gray-500">
            Phase {PHASES.findIndex((p) => p.key === phase.key) + 1} of {PHASES.length}
          </p>
        </div>
        <Pill tone="running">
          <StatusIcon status="running" size={12} /> Running
        </Pill>
      </div>

      <ProgressBar value={pct} label={`${phase.label} progress`} />

      <Block>
        <BlockHeader title="Now writing">
          {progress?.total ? (
            <span className="font-mono text-xs text-gray-500">
              {progress.current} / {progress.total}
            </span>
          ) : null}
        </BlockHeader>
        <BlockRow>
          <StatusIcon status="running" />
          <span className="min-w-0 flex-1 truncate font-mono">
            {progress?.lastItem || 'Preparing…'}
          </span>
        </BlockRow>
      </Block>
    </div>
  );
}

/** Builds the left-hand timeline from phase progress plus the error classes. */
function buildTimeline(
  phases: Record<string, PhaseProgress>,
  errors: ErrorsResponse | null,
  runState: RunState,
): TimelinePhase[] {
  return PHASES.map((phase, index) => {
    const progress = phases[phase.key];
    const classes = errors?.classes.filter((c) => c.phase === index + 1) ?? [];
    const hasOpen = classes.some((c) => c.open_count > 0);

    let state: PhaseState = 'pending';
    if (progress?.status === 'skipped') {
      state = 'skipped';
    } else if (progress?.status === 'completed' || progress?.status === 'completed_with_errors') {
      state = hasOpen ? 'failed' : 'done';
    } else if (progress?.status === 'started') {
      state = runState === 'paused' ? 'paused' : 'running';
    } else if (progress?.status === 'failed') {
      state = 'failed';
    }

    let summary: string | undefined;
    if (state === 'skipped') summary = progress?.reason ?? 'Nothing to migrate';
    else if (progress?.total) summary = `${progress.current} of ${progress.total}`;
    else if (state === 'done') summary = 'Complete';

    return {
      key: phase.key,
      name: phase.label,
      state,
      meta: state === 'running' ? 'running' : undefined,
      summary,
      classes,
    };
  });
}

function currentPhaseNumber(phases: Record<string, PhaseProgress>): number {
  const index = PHASES.findIndex((p) => phases[p.key]?.status === 'started');
  if (index >= 0) return index + 1;
  const done = PHASES.filter((p) => statusRank(phases[p.key]?.status) === 2).length;
  return Math.min(done + 1, PHASES.length);
}

function StageHeading({ children, done }: { children: React.ReactNode; done: boolean }) {
  return (
    <div className="mb-3 flex items-center justify-between border-b border-gray-75 pb-2">
      <span className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-500">{children}</span>
      {done && <Pill tone="done">Done</Pill>}
    </div>
  );
}

/**
 * The second stage: bring the projects an earlier partial moved into line with
 * the source. It writes only what differs, so its count is changes, not items.
 */
function PreviouslyMigrated({
  stage,
  projects,
  applied,
  failed,
  lastItem,
}: {
  stage: 'remaining' | 'previously_migrated' | 'done';
  projects: number;
  applied: number;
  failed: number;
  lastItem: string;
}) {
  const noun = projects === 1 ? 'project' : 'projects';
  return (
    <div className="mt-5">
      <div className="mb-2 flex items-center justify-between border-b border-gray-75 pb-2">
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-500">
          Previously Migrated
        </span>
        {stage === 'previously_migrated' && <Pill tone="running">Running</Pill>}
        {stage === 'done' && <Pill tone="done">Done</Pill>}
      </div>
      <div className={`flex items-start gap-2.5 text-[13px] ${stage === 'remaining' ? 'opacity-50' : ''}`}>
        {stage === 'previously_migrated' ? (
          <StatusIcon status="running" size={13} className="mt-0.5" />
        ) : stage === 'done' ? (
          <StatusIcon status={failed > 0 ? 'failed' : 'completed'} size={13} className="mt-0.5" />
        ) : (
          <RotateCw size={13} className="mt-0.5 shrink-0 text-gray-300" />
        )}
        <span className="min-w-0">
          <b className="block font-semibold text-gray-900">
            Bring {projects} earlier {noun} up to date
          </b>
          <span className="block truncate text-xs text-gray-500">
            {stage === 'remaining'
              ? 'After the remaining resources. Only what differs from the source is written.'
              : `${applied} ${applied === 1 ? 'change' : 'changes'} applied${failed > 0 ? ` · ${failed} failed` : ''}${
                  stage === 'previously_migrated' && lastItem ? ` · ${lastItem}` : ''
                }`}
          </span>
        </span>
      </div>
    </div>
  );
}
