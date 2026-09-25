import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  ArrowRight,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  Download,
  ListChecks,
  Loader2,
  RotateCw,
} from 'lucide-react';
import {
  Block,
  BlockHeader,
  Button,
  Callout,
  FooterBar,
  PageHeading,
  Pill,
} from '@/components/ui/primitives';
import { finalReportURL, setScope } from '@/api/client';
import type { ChangeGroup, Standby, StandbyRun } from '@/api/client';
import { useStandby } from '@/hooks/useStandby';
import { formatDay, formatWhen, timeAgo } from '@/lib/when';

/**
 * Waiting to cut over: home base while the source stays in use.
 *
 * It answers three questions in order — how current the target is, what has
 * been run, and what has to be finished before cutover — and starts either a
 * top-up or the cutover. Top Up is the one filled button because waiting is
 * what this page is for; Cut Over is not disabled here, because the Cutover
 * page already refuses while a token is undecided and says why.
 */
export default function StandbyPage() {
  const navigate = useNavigate();
  const { standby } = useStandby();
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function topUp() {
    setStarting(true);
    setError(null);
    try {
      // A top-up is always the whole source. Said explicitly, so a partial
      // selection left over from before cannot narrow it.
      await setScope({ mode: 'full', projects: [] }).catch(() => undefined);
      navigate('/gathering');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not start the top-up');
      setStarting(false);
    }
  }

  if (!standby) {
    return (
      <div className="flex items-center justify-center gap-2.5 py-20 text-[13px] text-gray-500">
        <Loader2 size={16} className="animate-spin-slow text-purple-500" />
        Loading…
      </div>
    );
  }

  if (standby.cutover_at) {
    return (
      <div className="mx-auto w-full max-w-[720px] px-6 py-6">
        <PageHeading title="Migration Finished" subtitle={`Cut over ${formatWhen(standby.cutover_at)}.`} />
        <div className="mt-5">
          <FooterBar
            right={
              <Button size="md" onClick={() => navigate('/complete')}>
                Open Summary <ArrowRight size={14} />
              </Button>
            }
          />
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-[820px] px-6 py-6">
      <PageHeading
        title="Waiting To Cut Over"
        subtitle="The source is still in use. Top up to bring its changes across, and cut over when teams are ready to switch."
      />

      <div className="mt-5 flex flex-col gap-3.5">
        {error && <Callout tone="error">{error}</Callout>}
        {!standby.configured && (
          <Callout tone="info">
            The tokens are kept in memory only, so after a restart they have to be entered again
            before a top-up or cutover can run. Set SOURCE_DOPPLER_TOKEN and TARGET_DOPPLER_TOKEN
            to have them picked up automatically.
            <span className="mt-2 block">
              <Button
                variant="secondary"
                onClick={() => navigate('/configure', { state: { returnTo: '/standby' } })}
              >
                Enter Tokens
              </Button>
            </span>
          </Callout>
        )}

        <SyncStrip standby={standby} />
        <RunsTable runs={standby.runs} />
        <BeforeCutover standby={standby} />

        <FooterBar
          left={
            <Button
              variant="ghost"
              onClick={() => {
                window.location.href = finalReportURL;
              }}
            >
              <Download size={14} /> Download Report
            </Button>
          }
          right={
            <>
              <Button
                variant="secondary"
                size="md"
                // Cutover plans from the source's ranges, so after a restart the
                // source is read first and Gather continues straight to it.
                onClick={() =>
                  standby.gathered ? navigate('/cutover') : navigate('/gathering', { state: { next: '/cutover' } })
                }
                disabled={starting}
              >
                Cut Over <ArrowRight size={14} />
              </Button>
              <Button size="md" onClick={topUp} disabled={starting}>
                <RotateCw size={14} /> Top Up Now
              </Button>
            </>
          }
        />
        <p className="-mt-1.5 text-right text-xs text-gray-500">
          Top Up reads the source and the target first. Nothing is written before you confirm on
          Review.
        </p>
      </div>
    </div>
  );
}

function SyncStrip({ standby }: { standby: Standby }) {
  const cells = [
    {
      value: timeAgo(standby.last_synced_at),
      label: `Target last brought up to date · ${formatWhen(standby.last_synced_at)}`,
    },
    { value: String(standby.top_ups), label: 'Top-ups since the full migration' },
    { value: formatDay(standby.full_at), label: 'Full migration' },
  ];
  return (
    <div className="grid grid-cols-1 overflow-hidden rounded-md border border-gray-75 bg-gray-0 sm:grid-cols-[1.2fr_1fr_1fr]">
      {cells.map((c) => (
        <div key={c.label} className="flex flex-col gap-0.5 border-b border-gray-75 px-4 py-3 last:border-b-0 sm:border-b-0 sm:border-r sm:last:border-r-0">
          <b className="text-[18px] font-semibold tabular-nums tracking-[-0.01em] text-gray-900">{c.value}</b>
          <span className="text-xs text-gray-500">{c.label}</span>
        </div>
      ))}
    </div>
  );
}

function runLabel(run: StandbyRun): string {
  if (run.kind === 'top_up') return `Top-up ${run.number ?? ''}`.trim();
  return run.kind === 'partial' ? 'Partial migration' : 'Full migration';
}

function RunsTable({ runs }: { runs: StandbyRun[] }) {
  const [open, setOpen] = useState<Set<number>>(() => new Set());
  const cols = 'grid-cols-[20px_130px_1fr_80px_80px_70px]';
  const toggle = (i: number) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(i)) next.delete(i);
      else next.add(i);
      return next;
    });

  return (
    <Block>
      <BlockHeader title="Runs">
        <Pill tone="waiting">{runs.length}</Pill>
      </BlockHeader>
      <div className="overflow-x-auto">
        <div className="min-w-[560px]">
          <div className={`grid ${cols} gap-3 border-b border-gray-75 bg-gray-50 px-3.5 py-2`}>
            <span />
            {['When', 'Run', 'Created', 'Updated', 'Failed'].map((h, i) => (
              <span
                key={h}
                className={`text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400 ${i >= 2 ? 'text-right' : ''}`}
              >
                {h}
              </span>
            ))}
          </div>
          {runs.map((run, i) => {
            const nothing = run.kind === 'top_up' && run.created === 0 && run.updated === 0 && run.failed === 0;
            const expanded = open.has(i);
            const panelId = `run-changes-${i}`;
            return (
              <div key={`${run.at}-${i}`} className="border-b border-gray-75 last:border-b-0">
                <button
                  type="button"
                  onClick={() => toggle(i)}
                  aria-expanded={expanded}
                  aria-controls={panelId}
                  className={`grid w-full ${cols} items-center gap-3 px-3.5 py-2 text-left text-[13px] hover:bg-gray-25`}
                >
                  <ChevronRight
                    size={13}
                    aria-hidden
                    className={`text-gray-400 transition-transform ${expanded ? 'rotate-90' : ''}`}
                  />
                  <span className="font-mono text-xs text-gray-600">{formatWhen(run.at)}</span>
                  <span className="text-gray-900">
                    {runLabel(run)}
                    {nothing && <span className="text-gray-500"> · nothing had changed</span>}
                  </span>
                  <span className="text-right font-mono text-xs tabular-nums text-gray-600">
                    {run.created.toLocaleString()}
                  </span>
                  <span className="text-right font-mono text-xs tabular-nums text-gray-600">
                    {run.kind === 'top_up' || run.updated > 0 ? run.updated.toLocaleString() : '—'}
                  </span>
                  <span
                    className={`text-right font-mono text-xs tabular-nums ${run.failed > 0 ? 'font-semibold text-red-600' : 'text-gray-600'}`}
                  >
                    {run.failed}
                  </span>
                </button>
                {expanded && (
                  <div id={panelId} className="border-t border-gray-75 bg-gray-25 px-3.5 py-3 pl-[46px]">
                    <RunChangesView run={run} nothing={nothing} />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </Block>
  );
}

/** Verbs for the counts beside each resource type. */
const ACTION_WORDS: [keyof Omit<ChangeGroup, 'label' | 'items' | 'more'>, string][] = [
  ['created', 'created'],
  ['updated', 'updated'],
  ['deleted', 'deleted'],
  ['migrated', 'migrated'],
  ['failed', 'failed'],
];

const ACTION_TONE: Record<string, string> = {
  created: 'text-purple-600',
  updated: 'text-yellow-600',
  deleted: 'text-gray-500',
  migrated: 'text-green-600',
  failed: 'text-red-600',
};

/**
 * What one run wrote, by resource type: the counts, then the items by name.
 * Names only — a secret is listed by name within its config, never with a
 * value — and capped, with the rest counted.
 */
function RunChangesView({ run, nothing }: { run: StandbyRun; nothing: boolean }) {
  if (nothing) {
    return <p className="text-[12.5px] text-gray-500">Nothing had changed on the source since the run before.</p>;
  }
  const groups = run.changes?.groups ?? [];
  if (groups.length === 0) {
    return (
      <p className="text-[12.5px] text-gray-500">
        Details were not recorded for this run. Runs record what they changed from this version on.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      {groups.map((g) => (
        <div key={g.label}>
          <div className="flex flex-wrap items-baseline gap-x-2 text-[12.5px]">
            <b className="font-semibold text-gray-900">{g.label}</b>
            <span className="text-gray-500">
              {ACTION_WORDS.filter(([k]) => g[k] > 0)
                .map(([k, word]) => `${g[k].toLocaleString()} ${word}`)
                .join(' · ')}
            </span>
          </div>
          <ul className="mt-1 flex flex-col gap-0.5">
            {g.items.map((it, j) => (
              <li key={j} className="flex items-baseline gap-2 text-xs">
                <span className={`w-[58px] shrink-0 ${ACTION_TONE[it.action] ?? 'text-gray-500'}`}>{it.action}</span>
                <span className="min-w-0 break-all font-mono text-gray-700">{it.item}</span>
              </li>
            ))}
            {(g.more ?? 0) > 0 && (
              <li className="pl-[66px] text-xs text-gray-500">and {g.more!.toLocaleString()} more</li>
            )}
          </ul>
        </div>
      ))}
    </div>
  );
}

function BeforeCutover({ standby }: { standby: Standby }) {
  const navigate = useNavigate();
  const manualTotal = standby.manual.reduce((n, m) => n + m.count, 0);
  const toFinish = (standby.tokens_pending > 0 ? 1 : 0) + (standby.open_errors > 0 ? 1 : 0);

  return (
    <Block>
      <BlockHeader title="Before Cutover">
        {toFinish > 0 ? (
          <Pill tone="warning">{toFinish} to finish</Pill>
        ) : (
          <Pill tone="done">Ready</Pill>
        )}
      </BlockHeader>
      <div className="flex flex-col">
        <Row
          icon={
            standby.tokens_pending > 0 ? (
              <CircleAlert size={15} className="text-yellow-500" />
            ) : (
              <CircleCheck size={15} className="text-green-500" />
            )
          }
          title={
            standby.tokens_pending > 0
              ? `${standby.tokens_pending} service ${standby.tokens_pending === 1 ? 'token' : 'tokens'} undecided${
                  standby.tokens_deferred ? ` (${standby.tokens_deferred} set aside for later)` : ''
                }`
              : 'Every service token decided'
          }
          detail={
            standby.tokens_pending > 0
              ? 'Cutover removes the access issuing one needs. Issue or skip them first.'
              : 'Issued on the target or skipped.'
          }
          action={
            standby.tokens_pending > 0 && (
              <Button variant="secondary" onClick={() => navigate('/service-tokens')}>
                Open Service Tokens
              </Button>
            )
          }
        />
        <Row
          icon={
            standby.open_errors > 0 ? (
              <CircleAlert size={15} className="text-red-500" />
            ) : (
              <CircleCheck size={15} className="text-green-500" />
            )
          }
          title={
            standby.open_errors > 0
              ? `${standby.open_errors} open ${standby.open_errors === 1 ? 'error' : 'errors'}`
              : 'No open errors'
          }
          detail={
            standby.open_errors > 0
              ? 'From the last run. A top-up retries what failed.'
              : 'The last run finished cleanly.'
          }
          action={
            standby.open_errors > 0 && (
              <Button variant="secondary" onClick={() => navigate('/migrate/errors')}>
                Open Triage
              </Button>
            )
          }
        />
        {manualTotal > 0 && (
          <Row
            icon={<ListChecks size={15} className="text-gray-400" />}
            title={`${manualTotal} ${manualTotal === 1 ? 'thing' : 'things'} to do by hand`}
            detail={`${standby.manual.map((m) => `${m.count} ${m.title.toLowerCase()}`).join(', ')}. Not needed before cutover.`}
            action={
              <Button variant="ghost" onClick={() => navigate('/manual-steps')}>
                View
              </Button>
            }
          />
        )}
      </div>
    </Block>
  );
}

function Row({
  icon,
  title,
  detail,
  action,
}: {
  icon: React.ReactNode;
  title: string;
  detail: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-3 border-b border-gray-75 px-3.5 py-2.5 text-[13px] last:border-b-0">
      <span className="flex w-4 shrink-0 justify-center">{icon}</span>
      <div className="min-w-0 flex-1">
        <b className="block font-semibold text-gray-900">{title}</b>
        <span className="block text-xs text-gray-500">{detail}</span>
      </div>
      {action}
    </div>
  );
}
