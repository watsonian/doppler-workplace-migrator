import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowRight, ArrowRightLeft, Loader2 } from 'lucide-react';
import {
  Block,
  Button,
  Callout,
  FooterBar,
  PageHeading,
  Pill,
  ProgressBar,
} from '@/components/ui/primitives';
import { getCompare, startCompare, subscribeSSE } from '@/api/client';
import type { CompareResponse, GatherProgress } from '@/api/client';
import type { ResourceKind } from '@/types/diff';
import { useWizard } from '@/hooks/useWizard';
import { isInFlight, useStandby } from '@/hooks/useStandby';

/**
 * Compare — what Gather becomes once the target already exists.
 *
 * Both sides on screen with the difference between them in the middle. A
 * resource type where the two match keeps its row, greyed, saying so: "nothing
 * to do here" is information, and the same rule the gather list follows for a
 * resource that came back empty.
 */

/** The rows, in the order they read. */
const ROWS: { kinds: string[]; label: string }[] = [
  { kinds: ['workplace_role', 'project_role'], label: 'Roles' },
  { kinds: ['group', 'group_members'], label: 'Groups' },
  { kinds: ['service_account'], label: 'Service accounts' },
  { kinds: ['project'], label: 'Projects' },
  { kinds: ['environment'], label: 'Environments' },
  { kinds: ['config'], label: 'Configs' },
  { kinds: ['secret'], label: 'Secrets' },
  { kinds: ['webhook'], label: 'Webhooks' },
  { kinds: ['project_member'], label: 'Project members' },
  { kinds: ['trusted_ips'], label: 'Trusted IP ranges' },
];

interface RowCounts {
  create: number;
  update: number;
  orphan: number;
  match: number;
}

export default function ComparePage() {
  const navigate = useNavigate();
  const { standby } = useStandby();
  const inFlight = isInFlight(standby);
  const { setBlockingError } = useWizard();

  const [result, setResult] = useState<CompareResponse | null>(null);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<GatherProgress | null>(null);
  const [error, setError] = useState<string | null>(null);

  const startedRef = useRef(false);
  const unsubRef = useRef<(() => void) | null>(null);

  const onEvent = useCallback((type: string, data: unknown) => {
    if (type === 'compare_progress') {
      setProgress(data as GatherProgress);
    } else if (type === 'compare_complete') {
      const payload = data as CompareResponse & { status: string; error?: string };
      setRunning(false);
      if (payload.status === 'failed') {
        setError(payload.error || 'The comparison could not be completed');
        return;
      }
      setResult(normalizeCompare(payload));
    }
  }, []);

  const start = useCallback(async () => {
    setRunning(true);
    setError(null);
    setResult(null);

    const sse = subscribeSSE(onEvent);
    unsubRef.current = sse.unsubscribe;

    try {
      const resp = await startCompare();
      // Dev mode answers synchronously: there is no target to read, so the
      // comparison is "everything is new" and comes straight back.
      // Told apart by its status, not its list: a list that came back empty
      // (or null) is still a finished comparison.
      if ((resp as unknown as { status?: string }).status === 'complete') {
        setResult(normalizeCompare(resp as unknown as CompareResponse));
        setRunning(false);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not start the comparison');
      setRunning(false);
    }
  }, [onEvent]);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;

    void (async () => {
      try {
        const existing = (await getCompare()) as CompareResponse & { status: string };
        if (existing.status === 'running') {
          setRunning(true);
          const sse = subscribeSSE(onEvent);
          unsubRef.current = sse.unsubscribe;
          return;
        }
        if (existing.status === 'complete') {
          setResult(normalizeCompare(existing));
          return;
        }
      } catch {
        // Nothing to attach to; start one.
      }
      await start();
    })();

    return () => {
      unsubRef.current?.();
      unsubRef.current = null;
    };
  }, [start, onEvent]);

  useEffect(() => {
    setBlockingError(Boolean(error));
  }, [error, setBlockingError]);

  const rows = result ? buildRows(result) : null;
  const drift = result?.drift ?? [];

  return (
    <div className="mx-auto w-full max-w-[880px] px-6 py-6">
      <PageHeading
        title={result?.target_empty ? 'Nothing On The Target Yet' : 'Comparing Source And Target'}
        subtitle={
          running
            ? 'Reading both instances. Nothing is written.'
            : result?.target_empty
              ? 'This is a first migration — everything will be created.'
              : 'Reading both instances. Nothing is written.'
        }
      />

      {running && (
        <div className="mx-auto mt-3 flex max-w-[360px] flex-col items-center gap-2">
          <ProgressBar value={35} label="Compare progress" className="w-full" />
          <span className="text-[12.5px] text-gray-500">
            {progress?.message ?? 'Reading the target…'}
          </span>
        </div>
      )}

      <div className="mt-5 flex flex-col gap-3.5">
        {error && (
          <Callout tone="error">
            <p className="mb-2">{error}</p>
            <Button variant="secondary" onClick={start}>
              Try Again
            </Button>
          </Callout>
        )}

        {rows && (
          <Block>
            <div className="grid grid-cols-[1fr_170px_1fr] border-b border-gray-75 bg-gray-50">
              <span className="px-3.5 py-2.5 text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">
                Source
              </span>
              <span className="px-3.5 py-2.5 text-center text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">
                Difference
              </span>
              <span className="px-3.5 py-2.5 text-right text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">
                Target
              </span>
            </div>

            {rows.map((row) => {
              const identical = row.create === 0 && row.update === 0 && row.orphan === 0;
              return (
                <div
                  key={row.label}
                  className={`grid grid-cols-[1fr_170px_1fr] border-b border-gray-75 text-[13px] last:border-b-0 ${
                    identical ? 'opacity-50' : ''
                  }`}
                >
                  <span className="flex items-center gap-2 px-3.5 py-2">{row.label}</span>
                  <span className="flex items-center justify-center gap-1.5 border-x border-gray-75 bg-gray-25 px-2 py-2">
                    {identical ? (
                      <Pill tone="skipped">Identical</Pill>
                    ) : (
                      <>
                        {row.create > 0 && <Pill tone="running">+{row.create}</Pill>}
                        {row.update > 0 && <Pill tone="warning">~{row.update}</Pill>}
                        {row.orphan > 0 && <Pill tone="error">−{row.orphan}</Pill>}
                      </>
                    )}
                  </span>
                  <span className="flex items-center justify-end px-3.5 py-2 tabular-nums text-gray-500">
                    {row.match > 0 ? `${row.match.toLocaleString()} already current` : '—'}
                  </span>
                </div>
              );
            })}
          </Block>
        )}

        {result?.drift_unavailable && (
          <Callout tone="warning">
            <span className="font-semibold text-gray-900">
              Changes made on the target could not be checked.
            </span>{' '}
            {result.drift_unavailable}
          </Callout>
        )}

        {drift.length > 0 && (
          <Callout tone="info">
            <span className="font-semibold text-gray-900">
              {drift.length} {drift.length === 1 ? 'config was' : 'configs were'} edited on the
              target since this migrator last wrote.
            </span>{' '}
            From the config logs — {describeActors(drift)}. Listed separately on the next screen
            rather than folded in with source changes.
          </Callout>
        )}

        {result && !result.target_empty && drift.length === 0 && !result.drift_unavailable && (
          <Callout tone="success">
            Nothing has been edited on the target since this migrator last wrote. Everything
            different came from the source.
          </Callout>
        )}

        <FooterBar
          left={
            <Button variant="ghost" onClick={() => navigate(inFlight ? '/standby' : '/scope')} disabled={running}>
              Back
            </Button>
          }
          right={
            <Button size="md" disabled={!result || running} onClick={() => navigate('/plan')}>
              Continue <ArrowRight size={14} />
            </Button>
          }
        />
      </div>

      {running && (
        <div className="mt-6 flex items-center justify-center gap-2 text-[12.5px] text-gray-400">
          <Loader2 size={13} className="animate-spin-slow" />
          <ArrowRightLeft size={13} />
          Comparing
        </div>
      )}
    </div>
  );
}

/**
 * Lists as lists. Go encodes one nothing was appended to as null, and a
 * comparison that found no differences — the result a top-up most hopes for —
 * crashed this page looping over it. The server sends [] now; this keeps the
 * page from depending on it.
 */
function normalizeCompare(r: CompareResponse): CompareResponse {
  return { ...r, differences: r.differences ?? [], matched: r.matched ?? {}, drift: r.drift ?? [] };
}

/** Rolls the flat difference list up into one row per resource type. */
export function buildRows(result: CompareResponse): (RowCounts & { label: string })[] {
  return ROWS.map(({ kinds, label }) => {
    const counts: RowCounts = { create: 0, update: 0, orphan: 0, match: 0 };
    for (const diff of result.differences) {
      if (!kinds.includes(diff.kind)) continue;
      if (diff.disposition === 'create') counts.create++;
      if (diff.disposition === 'update') counts.update++;
      if (diff.disposition === 'orphan') counts.orphan++;
    }
    // A new project's contents are additions too, counted on the project
    // rather than listed one by one.
    for (const diff of result.differences) {
      if (diff.disposition !== 'create' || !diff.contains) continue;
      for (const kind of kinds) {
        counts.create += diff.contains[kind as ResourceKind] ?? 0;
      }
    }
    for (const kind of kinds) {
      counts.match += result.matched[kind] ?? 0;
    }
    return { ...counts, label };
  });
}

/**
 * Summarises who did the editing. Whether it was a person or another tool is
 * the part that changes what you do about it, so it goes in the sentence rather
 * than waiting to be discovered in the table.
 */
function describeActors(drift: { changed_by: string }[]): string {
  const actors = [...new Set(drift.map((d) => d.changed_by).filter(Boolean))];
  if (actors.length === 0) return 'actor unknown';
  if (actors.length === 1) return `all by ${actors[0]}`;
  if (actors.length === 2) return `by ${actors[0]} and ${actors[1]}`;
  return `by ${actors[0]} and ${actors.length - 1} others`;
}
