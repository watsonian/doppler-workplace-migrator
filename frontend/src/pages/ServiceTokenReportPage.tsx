import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useStandby } from '@/hooks/useStandby';
import {
  ArrowLeft,
  ArrowRight,
  ArrowRightCircle,
  Clock,
  Download,
  KeyRound,
  Loader2,
  MinusCircle,
  RotateCw,
  SkipForward,
} from 'lucide-react';
import {
  Block,
  Button,
  Callout,
  PageHeading,
  Pill,
  StatusIcon,
} from '@/components/ui/primitives';
import {
  RerollDialog,
  RevealDialog,
  SkipAllDialog,
  SkipDialog,
  TokenFailureDialog,
} from '@/components/tokens/TokenDialogs';
import {
  APIError,
  detectExistingTokens,
  getTokens,
  issueToken,
  rerollToken,
  skipAllTokens,
  skipToken,
  tokenWorklistURL,
  undoTokenSkip,
  deferToken,
} from '@/api/client';
import type { IssuedToken, TokenCounts, TokenRecord } from '@/api/client';
import { tokenPlacement } from '@/lib/tokenPlacement';

/**
 * The service token worklist.
 *
 * Every row ends issued or skipped, and cutover will not run until they all
 * have. That is not bureaucracy: issuing needs config write access, cutover
 * removes it, and nothing can restore it — a second cutover finds the ranges
 * already mirrored and does nothing. An undecided token at that point is a
 * decision nobody can ever make again.
 *
 * The value of an issued token lives in `revealed` for as long as its dialog is
 * open and is then dropped. It is never written anywhere else.
 */
export default function ServiceTokenReportPage() {
  const navigate = useNavigate();
  const { standby } = useStandby();

  const [records, setRecords] = useState<TokenRecord[]>([]);
  const [counts, setCounts] = useState<TokenCounts | null>(null);
  const [notYet, setNotYet] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<IssuedToken | null>(null);
  const [skipping, setSkipping] = useState<TokenRecord | null>(null);
  const [rerolling, setRerolling] = useState<TokenRecord | null>(null);
  const [skippingAll, setSkippingAll] = useState(false);
  const [failure, setFailure] = useState<{ record: TokenRecord; message: string; status?: number } | null>(
    null,
  );

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await getTokens();
      setRecords(resp.records ?? []);
      setCounts(resp.counts);
      setNotYet(resp.not_yet_migrated ?? 0);

      // A separate request because it costs one list call per scope that still
      // has an undecided token. A failure here only means the page cannot warn
      // about duplicates, so it is not surfaced as a page error.
      try {
        const detected = await detectExistingTokens();
        setRecords(detected.records ?? []);
        setCounts(detected.counts);
      } catch {
        /* nothing to say: the worklist above is still correct */
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load the service tokens');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  function apply(resp: { records: TokenRecord[]; counts: TokenCounts }) {
    setRecords(resp.records ?? []);
    setCounts(resp.counts);
  }

  async function runIssue(record: TokenRecord, reroll: boolean) {
    setBusyKey(record.key);
    setRerolling(null);
    try {
      const resp = reroll ? await rerollToken(record.key) : await issueToken(record.key);
      setCounts(resp.counts);
      setRevealed(resp.token);
      // The row's own state changed; refresh it without disturbing the dialog.
      void getTokens().then((r) => setRecords(r.records ?? [])).catch(() => {});
    } catch (err) {
      setFailure({
        record,
        message: err instanceof Error ? err.message : 'The token could not be issued',
        status: err instanceof APIError ? err.status : undefined,
      });
    } finally {
      setBusyKey(null);
    }
  }

  async function runSkip(record: TokenRecord, notNeeded: boolean) {
    setBusyKey(record.key);
    try {
      apply(await skipToken(record.key, notNeeded));
      setSkipping(null);
      setFailure(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not skip the token');
    } finally {
      setBusyKey(null);
    }
  }

  async function runUndo(record: TokenRecord) {
    setBusyKey(record.key);
    try {
      apply(await undoTokenSkip(record.key));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not undo the skip');
    } finally {
      setBusyKey(null);
    }
  }

  async function runDefer(record: TokenRecord) {
    setBusyKey(record.key);
    try {
      apply(await deferToken(record.key));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not set the token aside');
    } finally {
      setBusyKey(null);
    }
  }

  async function runSkipAll() {
    setBusyKey('__all__');
    try {
      apply(await skipAllTokens());
      setSkippingAll(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not skip the remaining tokens');
    } finally {
      setBusyKey(null);
    }
  }

  const serviceTokens = useMemo(
    () => records.filter((r) => r.kind === 'service_token'),
    [records],
  );
  const accountTokens = useMemo(
    () => records.filter((r) => r.kind === 'service_account_token'),
    [records],
  );

  const deferred = counts?.deferred ?? 0;
  const handled = counts ? counts.total - counts.pending - deferred : 0;
  // Decided means neither undecided nor set aside: cutover waits on both.
  const allHandled = Boolean(counts && counts.pending === 0 && deferred === 0 && counts.total > 0);
  // Nothing to do now, with some set aside for a later run.
  const onlySetAside = Boolean(counts && counts.pending === 0 && deferred > 0);

  // Where this page leads depends on where the migration is. After a run,
  // the next step is the choice the Summary or the waiting page offers —
  // run the full migration, or cut over or wait — not cutover itself. A dry
  // run records no runs, so there is nothing to choose between.
  const next: { label: string; to: string; disabled: boolean; back?: { label: string; to: string } } =
    standby?.in_flight && standby.waiting
      ? { label: 'Back To Waiting', to: '/standby', disabled: false, back: { label: 'Back To Summary', to: '/complete' } }
      : standby && (standby.runs?.length ?? 0) > 0 && !standby.cutover_at
        ? { label: 'Continue', to: '/complete', disabled: false }
        : { label: 'Continue To Cutover', to: '/cutover', disabled: !allHandled, back: { label: 'Back To Summary', to: '/complete' } };
  const worklistSize = counts ? counts.to_recreate : 0;

  if (loading) {
    return (
      <div className="flex items-center justify-center gap-2.5 py-20 text-[13px] text-gray-500">
        <Loader2 size={16} className="animate-spin-slow text-purple-500" />
        Loading service tokens…
      </div>
    );
  }

  const rowProps = { busyKey, onIssue: (r: TokenRecord) => runIssue(r, false), onSkip: setSkipping, onReroll: setRerolling, onUndo: runUndo, onDefer: runDefer };

  return (
    <div className="mx-auto w-full max-w-[940px] px-6 py-6">
      <PageHeading
        title="Service Tokens"
        subtitle="Issue each of these on the target with the access and placement it had on the source, or skip it onto a worklist to recreate by hand. The value is shown once, at issue."
      />

      <div className="mt-5 flex flex-col gap-3.5">
        {/* After a partial run, tokens for the projects it left out have nowhere
            on the target to go yet. Said, rather than silently missing. */}
        {notYet > 0 && (
          <Callout tone="info">
            {notYet} more {notYet === 1 ? 'token belongs' : 'tokens belong'} to projects or service
            accounts not migrated yet. {notYet === 1 ? 'It appears' : 'They appear'} here once a run
            migrates {notYet === 1 ? 'its project' : 'them'}.
          </Callout>
        )}
        {error && (
          <Callout tone="error">
            <p className="mb-2">{error}</p>
            <Button variant="secondary" onClick={load}>
              Try Again
            </Button>
          </Callout>
        )}

        {counts && counts.total === 0 && (
          <Callout tone="success">
            There are no service tokens on the source, so there is nothing to issue or hand over.
          </Callout>
        )}

        {onlySetAside && (
          <Callout tone="info">
            <span className="font-semibold text-gray-900">Nothing to decide now.</span>{' '}
            {deferred} {deferred === 1 ? 'token is' : 'tokens are'} set aside for later and{' '}
            {deferred === 1 ? 'comes' : 'come'} back undecided on the next top-up. Cutover waits until{' '}
            {deferred === 1 ? 'it is' : 'they are'} issued or skipped.
          </Callout>
        )}

        {counts && counts.total > 0 && !allHandled && !onlySetAside && (
          <Callout tone="warning">
            <span className="font-semibold text-gray-900">Handle these before cutover.</span>{' '}
            Issuing a token is a config write, so it passes the trusted-IP gate. Cutover will not
            start until every row here is issued or skipped.
          </Callout>
        )}

        {allHandled && (
          <Callout tone="success">
            <span className="font-semibold text-gray-900">Every token has been decided.</span>{' '}
            {counts!.issued} issued here{counts!.to_recreate > 0 && `, ${counts!.to_recreate} on the worklist to recreate by hand`}
            {counts!.not_needed > 0 && `, ${counts!.not_needed} not needed`}. None of the values
            were kept.
          </Callout>
        )}

        {counts && counts.total > 0 && <ProgressStrip counts={counts} handled={handled} />}

        {serviceTokens.length > 0 && (
          <Block>
            <div className="grid grid-cols-[20px_1fr_92px_104px_214px] items-center gap-3 border-b border-gray-75 bg-gray-50 px-3.5 py-2.5">
              <span />
              <ColumnLabel>Token</ColumnLabel>
              <ColumnLabel>Access</ColumnLabel>
              <ColumnLabel>Expires</ColumnLabel>
              <ColumnLabel align="right">Action</ColumnLabel>
            </div>
            {serviceTokens.map((rec) => (
              <TokenRow key={rec.key} record={rec} {...rowProps} />
            ))}
          </Block>
        )}

        {accountTokens.length > 0 && (
          <div>
            <h2 className="mb-1 text-[15px] font-semibold text-gray-900">Service Account Tokens</h2>
            <p className="mb-2.5 max-w-[70ch] text-[12.5px] text-gray-500">
              These belong to a service account rather than a config, and take their permissions
              from that account's workplace role. Issuing one here creates it under the matching
              account on the target.
            </p>
            <Block>
              <div className="grid grid-cols-[20px_1fr_104px_214px] items-center gap-3 border-b border-gray-75 bg-gray-50 px-3.5 py-2.5">
                <span />
                <ColumnLabel>Token</ColumnLabel>
                <ColumnLabel>Expires</ColumnLabel>
                <ColumnLabel align="right">Action</ColumnLabel>
              </div>
              {accountTokens.map((rec) => (
                <TokenRow key={rec.key} record={rec} accountTable {...rowProps} />
              ))}
            </Block>
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-gray-75 pt-3.5">
          {next.back ? (
            <Button variant="ghost" onClick={() => navigate(next.back!.to)}>
              <ArrowLeft size={13} /> {next.back.label}
            </Button>
          ) : (
            <span />
          )}
          <div className="flex flex-wrap items-center gap-2">
            {counts && counts.pending > 0 && (
              <Button variant="ghost" onClick={() => setSkippingAll(true)}>
                <SkipForward size={13} /> Skip All Remaining
              </Button>
            )}
            {worklistSize > 0 && (
              <Button
                variant="secondary"
                onClick={() => {
                  window.location.href = tokenWorklistURL;
                }}
              >
                <Download size={13} /> Export Worklist
              </Button>
            )}
            <Button size="md" disabled={next.disabled} onClick={() => navigate(next.to)}>
              {next.label} <ArrowRight size={14} />
            </Button>
          </div>
        </div>

        {counts && counts.pending + (counts.deferred ?? 0) > 0 && (
          <p className="text-right text-[12px] text-gray-500">
            {[
              counts.pending > 0 && `${counts.pending} still undecided`,
              (counts.deferred ?? 0) > 0 && `${counts.deferred} set aside for later`,
            ]
              .filter(Boolean)
              .join(' and ')}
            , so cutover is not available yet.
            {(counts.deferred ?? 0) > 0 && ' Tokens set aside come back undecided on the next top-up.'}
          </p>
        )}
      </div>

      {revealed && <RevealDialog token={revealed} onClose={() => setRevealed(null)} />}

      {skipping && (
        <SkipDialog
          record={skipping}
          busy={busyKey === skipping.key}
          onSkip={(notNeeded) => runSkip(skipping, notNeeded)}
          onClose={() => setSkipping(null)}
        />
      )}

      {rerolling && (
        <RerollDialog
          record={rerolling}
          busy={busyKey === rerolling.key}
          onConfirm={() => runIssue(rerolling, true)}
          onClose={() => setRerolling(null)}
        />
      )}

      {skippingAll && counts && (
        <SkipAllDialog
          pending={counts.pending}
          busy={busyKey === '__all__'}
          onConfirm={runSkipAll}
          onClose={() => setSkippingAll(false)}
        />
      )}

      {failure && (
        <TokenFailureDialog
          record={failure.record}
          message={failure.message}
          status={failure.status}
          busy={busyKey === failure.record.key}
          onSkip={() => runSkip(failure.record, false)}
          onClose={() => setFailure(null)}
        />
      )}
    </div>
  );
}

function ColumnLabel({ children, align }: { children: React.ReactNode; align?: 'right' }) {
  return (
    <span
      className={`text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400 ${
        align === 'right' ? 'text-right' : ''
      }`}
    >
      {children}
    </span>
  );
}

/**
 * Two-toned, because a workplace that issued nothing and skipped everything has
 * finished the job. A bar that only filled on "issued" would read as 0% done.
 */
function ProgressStrip({ counts, handled }: { counts: TokenCounts; handled: number }) {
  const pct = (n: number) => (counts.total > 0 ? (n / counts.total) * 100 : 0);
  const skipped = counts.to_recreate + counts.not_needed;

  return (
    <div className="flex items-center gap-3.5 rounded-md border border-gray-75 bg-gray-0 px-3.5 py-2.5">
      <span className="whitespace-nowrap font-mono text-[13px] tabular-nums text-gray-900">
        {handled} of {counts.total} handled
      </span>
      <span className="flex h-1.5 flex-1 overflow-hidden rounded-full bg-gray-75">
        <span className="h-full bg-green-500" style={{ width: `${pct(counts.issued)}%` }} />
        <span className="h-full bg-gray-300" style={{ width: `${pct(skipped)}%` }} />
      </span>
      <span className="flex gap-3 whitespace-nowrap text-[11.5px] text-gray-500">
        <span>
          <i className="mr-1 inline-block h-2 w-2 rounded-sm bg-green-500 align-middle" />
          issued {counts.issued}
        </span>
        <span>
          <i className="mr-1 inline-block h-2 w-2 rounded-sm bg-gray-300 align-middle" />
          skipped {skipped}
        </span>
        {(counts.deferred ?? 0) > 0 && (
          <span>
            <Clock size={9} className="mr-1 inline-block align-middle" />
            later {counts.deferred}
          </span>
        )}
      </span>
    </div>
  );
}

interface RowProps {
  record: TokenRecord;
  accountTable?: boolean;
  busyKey: string | null;
  onIssue: (r: TokenRecord) => void;
  onSkip: (r: TokenRecord) => void;
  onReroll: (r: TokenRecord) => void;
  onUndo: (r: TokenRecord) => void;
  onDefer: (r: TokenRecord) => void;
}

function TokenRow({ record, accountTable, busyKey, onIssue, onSkip, onReroll, onUndo, onDefer }: RowProps) {
  const busy = busyKey === record.key;
  const scope = tokenPlacement(record, ' / ');

  const tone =
    record.disposition === 'issued'
      ? 'bg-green-50'
      : record.disposition === 'not_needed'
        ? 'bg-gray-25 opacity-70'
        : record.disposition === 'to_recreate' || record.disposition === 'deferred'
          ? 'bg-gray-25'
          : record.note
            ? 'bg-red-50'
            : '';

  const columns = accountTable
    ? 'grid-cols-[20px_1fr_104px_278px]'
    : 'grid-cols-[20px_1fr_92px_104px_278px]';

  return (
    <div className={`grid ${columns} items-center gap-3 border-b border-gray-75 px-3.5 py-2.5 text-[13px] last:border-b-0 ${tone}`}>
      <RowIcon record={record} busy={busy} />

      <div className="min-w-0">
        <b className="block truncate text-[13px] font-semibold text-gray-900">{record.name}</b>
        <em
          className={`block truncate text-[11.5px] not-italic text-gray-500 ${accountTable ? '' : 'font-mono'}`}
        >
          {scope}
        </em>
      </div>

      {!accountTable && (
        <span>
          {record.access && <Pill tone="skipped">{record.access}</Pill>}
        </span>
      )}

      <span className="font-mono text-[11.5px] text-gray-500">
        {formatExpiry(record.source_expires_at)}
      </span>

      <div className="flex items-center justify-end gap-1.5">
        <RowActions
          record={record}
          busy={busy}
          onIssue={onIssue}
          onSkip={onSkip}
          onReroll={onReroll}
          onUndo={onUndo}
          onDefer={onDefer}
        />
      </div>

      {record.note && (
        <div
          className={`col-start-2 col-end-[-1] mt-0.5 flex items-center gap-1.5 text-[11.5px] ${
            record.disposition === 'pending' ? 'text-red-600' : 'text-gray-600'
          }`}
        >
          {record.expiry_shortened ? <Clock size={10} /> : <MinusCircle size={10} />}
          {record.note}
          {/* When, so an old failure cannot pass for a new one. */}
          {record.failed_at && <span className="text-gray-500">· tried at {formatTime(record.failed_at)}</span>}
        </div>
      )}
    </div>
  );
}

function RowIcon({ record, busy }: { record: TokenRecord; busy: boolean }) {
  if (busy) return <Loader2 size={11} className="animate-spin-slow text-purple-500" />;
  switch (record.disposition) {
    case 'issued':
      return <StatusIcon status="completed" size={11} />;
    case 'to_recreate':
      return <ArrowRightCircle size={11} className="text-gray-400" />;
    case 'not_needed':
      return <MinusCircle size={10} className="text-gray-400" />;
    case 'deferred':
      return <Clock size={11} className="text-gray-400" />;
    default:
      return record.note ? (
        <StatusIcon status="failed" size={11} />
      ) : (
        <StatusIcon status="pending" size={7} />
      );
  }
}

function RowActions({
  record,
  busy,
  onIssue,
  onSkip,
  onReroll,
  onUndo,
  onDefer,
}: Omit<RowProps, 'accountTable' | 'busyKey'> & { busy: boolean }) {
  if (busy) {
    return (
      <Button variant="secondary" disabled>
        Working…
      </Button>
    );
  }

  switch (record.disposition) {
    case 'issued':
      return (
        <>
          <span className="whitespace-nowrap font-mono text-[11px] text-gray-500">
            {formatTime(record.issued_at)}
          </span>
          <Button variant="ghost" onClick={() => onReroll(record)}>
            <RotateCw size={13} /> Reroll
          </Button>
        </>
      );

    case 'to_recreate':
      return (
        <>
          <Pill tone="skipped">On worklist</Pill>
          <Button variant="ghost" onClick={() => onUndo(record)}>
            Undo
          </Button>
        </>
      );

    case 'not_needed':
      return <Pill tone="skipped">Not needed</Pill>;

    case 'deferred':
      return (
        <>
          <span title="Comes back undecided on the next top-up">
            <Pill tone="waiting">
              <Clock size={11} /> Later
            </Pill>
          </span>
          <Button variant="ghost" onClick={() => onUndo(record)}>
            Undo
          </Button>
        </>
      );

    default:
      // A token already on the target under this name would be duplicated, not
      // replaced, so the only safe forward action is a reroll.
      if (record.preexisting_on_target) {
        return (
          <>
            <Pill tone="skipped">On target</Pill>
            <Button variant="secondary" onClick={() => onReroll(record)}>
              <RotateCw size={13} /> Reroll
            </Button>
          </>
        );
      }
      return (
        <>
          <Button variant="ghost" onClick={() => onSkip(record)}>
            Skip
          </Button>
          {/* Neither issued nor skipped: set aside, and offered again on the
              next top-up. Cutover still waits on it. */}
          <Button variant="ghost" onClick={() => onDefer(record)} title="Set aside until the next top-up">
            Later
          </Button>
          <Button variant="secondary" onClick={() => onIssue(record)}>
            <KeyRound size={13} /> Issue Token
          </Button>
        </>
      );
  }
}

function formatExpiry(value?: string | null): string {
  if (!value) return 'Never';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toISOString().slice(0, 10);
}

function formatTime(value?: string | null): string {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString(undefined, { hour12: false });
}
