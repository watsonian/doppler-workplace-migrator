import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowRight, ChevronDown, ChevronRight, RotateCw } from 'lucide-react';
import {
  Button,
  Callout,
  Eyebrow,
  FooterBar,
  PageHeading,
  Pill,
  ProgressBar,
  StatusIcon,
  TextField,
} from '@/components/ui/primitives';
import EvidencePanel from '@/components/errors/EvidencePanel';
import { configure, getConfiguration, runPreflight, subscribeSSE } from '@/api/client';
import type { PreflightCheck } from '@/types/migration';
import { useWizard } from '@/hooks/useWizard';
import { isInFlight, useStandby } from '@/hooks/useStandby';

/** Checks about what the source token may read, which the permissions page explains. */
const SOURCE_TOKEN_CHECKS = new Set(['source_permissions', 'source_restricted_secrets']);

/**
 * Pre-flight — where an error class gets caught before it exists.
 *
 * A blocking check renders the same three things a run-time failure does: the
 * cause, the exchange that proves it, and the fix. That is deliberate. The
 * largest class of failures in a real migration is knowable from the tokens and
 * the two workplaces, and catching it here means nobody has to triage it later.
 * Making the two screens look the same means the remedy is learned once.
 */
export default function PreflightPage() {
  const navigate = useNavigate();
  const { standby } = useStandby();
  const inFlight = isInFlight(standby);
  const { setBlockingError } = useWizard();

  const [checks, setChecks] = useState<PreflightCheck[]>([]);
  const [running, setRunning] = useState(false);
  const [completed, setCompleted] = useState(false);
  const [passed, setPassed] = useState(false);
  const [hasWarnings, setHasWarnings] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);

  const unsubRef = useRef<(() => void) | null>(null);
  const startedRef = useRef(false);

  const onEvent = useCallback((type: string, data: unknown) => {
    if (type === 'preflight_check') {
      const check = data as PreflightCheck;
      setChecks((prev) => {
        const idx = prev.findIndex((c) => c.id === check.id);
        if (idx < 0) return [...prev, check];
        const next = [...prev];
        next[idx] = check;
        return next;
      });
    }
  }, []);

  const start = useCallback(async () => {
    setRunning(true);
    setCompleted(false);
    setError(null);
    setChecks([]);

    const sse = subscribeSSE(onEvent);
    unsubRef.current = sse.unsubscribe;

    try {
      const response = await runPreflight();
      setChecks(response.checks);
      setPassed(response.passed);
      setHasWarnings(response.has_warnings);
      setCompleted(true);
      // Expand the first blocking check. It is the thing that has to be dealt
      // with, and making someone hunt for it is a waste of their attention.
      const blocking = response.checks.find((c) => c.status === 'failed');
      if (blocking) setExpanded(new Set([blocking.id]));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'The checks could not be run');
    } finally {
      setRunning(false);
      unsubRef.current?.();
      unsubRef.current = null;
    }
  }, [onEvent]);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    void start();
    return () => {
      unsubRef.current?.();
      unsubRef.current = null;
    };
  }, [start]);

  useEffect(() => {
    setBlockingError(completed && !passed);
  }, [completed, passed, setBlockingError]);

  const failedCount = checks.filter((c) => c.status === 'failed').length;
  const warningCount = checks.filter((c) => c.status === 'warning').length;
  const passedCount = checks.filter((c) => c.status === 'passed').length;
  const doneCount = checks.filter((c) => c.status !== 'running' && c.status !== 'pending').length;

  return (
    <div className="mx-auto w-full max-w-[720px] px-6 py-6">
      <PageHeading
        title="Pre-flight Checks"
        subtitle={
          completed
            ? `${checks.length} checks · ${passedCount} passed, ${warningCount} ${warningCount === 1 ? 'warning' : 'warnings'}, ${failedCount} blocking.`
            : `Checking things that would otherwise fail mid-run. ${doneCount} of ${checks.length || '…'} done.`
        }
      />

      {running && checks.length > 0 && (
        <ProgressBar
          className="mx-auto mt-3 max-w-[300px]"
          value={(doneCount / Math.max(checks.length, 1)) * 100}
          label="Pre-flight progress"
        />
      )}

      <div className="mt-5 flex flex-col gap-3.5">
        {error && <Callout tone="error">{error}</Callout>}

        {completed && passed && !hasWarnings && (
          <Callout tone="success">
            Nothing here would have failed during the run. Errors can still appear from target state
            at write time — a name collision, a rate limit — and those surface in the migration view.
          </Callout>
        )}

        <div className="overflow-hidden rounded-md border border-gray-75 bg-gray-0">
          {checks.map((check) => (
            <CheckRow
              key={check.id}
              check={check}
              expanded={expanded.has(check.id)}
              onToggle={() =>
                setExpanded((prev) => {
                  const next = new Set(prev);
                  if (next.has(check.id)) next.delete(check.id);
                  else next.add(check.id);
                  return next;
                })
              }
              onFixed={start}
            />
          ))}
          {checks.length === 0 && (
            <div className="px-4 py-6 text-center text-[13px] text-gray-500">
              Running the checks…
            </div>
          )}
        </div>

        <FooterBar
          left={
            <Button variant="secondary" onClick={start} disabled={running}>
              <RotateCw size={13} /> {running ? 'Checking…' : 'Re-check All'}
            </Button>
          }
          right={
            <Button
              size="md"
              // While waiting to cut over, the next thing is the waiting page:
              // a top-up gathers when it starts, not before.
              onClick={() => navigate(inFlight ? '/standby' : '/gathering')}
              disabled={!completed || failedCount > 0}
            >
              Continue <ArrowRight size={14} />
            </Button>
          }
        />

        {completed && failedCount > 0 && (
          <p className="text-right text-[12.5px] text-gray-500">
            Fix the blocking {failedCount === 1 ? 'check' : 'checks'} above, or re-check, before
            continuing.
          </p>
        )}
      </div>
    </div>
  );
}

function CheckRow({
  check,
  expanded,
  onToggle,
  onFixed,
}: {
  check: PreflightCheck;
  expanded: boolean;
  onToggle: () => void;
  onFixed: () => void;
}) {
  const blocking = check.status === 'failed';
  const expandable = Boolean(check.detail || check.evidence || check.scope || check.fix);

  return (
    <div className={blocking ? 'bg-red-50' : ''}>
      <button
        type="button"
        onClick={expandable ? onToggle : undefined}
        aria-expanded={expandable ? expanded : undefined}
        className={`flex w-full items-start gap-2.5 border-b border-gray-75 px-3.5 py-3 text-left ${
          expandable ? 'hover:bg-gray-25' : 'cursor-default'
        }`}
      >
        <StatusIcon status={check.status} className="mt-0.5" />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5 text-[13px] font-semibold text-gray-900">
            {check.name}
            {expandable &&
              (expanded ? (
                <ChevronDown size={11} className="text-gray-400" />
              ) : (
                <ChevronRight size={11} className="text-gray-400" />
              ))}
          </span>
          {check.message && <span className="block text-[12.5px] text-gray-500">{check.message}</span>}
          {check.scope && (
            <span className="block text-[12.5px] text-gray-500">{check.scope.description}</span>
          )}
        </span>
        <StatusPill status={check.status} />
      </button>

      {expanded && expandable && (
        <div
          className={`flex flex-col gap-3 border-b border-gray-75 px-3.5 pb-4 pl-10 ${
            blocking ? 'bg-red-50' : 'bg-gray-25'
          }`}
        >
          {check.detail && (
            <Callout tone={blocking ? 'error' : 'warning'} className="bg-gray-0">
              {check.detail}
              {/* The source token's permissions are explained, with how to set
                  them up, on a page of their own. */}
              {SOURCE_TOKEN_CHECKS.has(check.id) && (
                <Link to="/permissions" className="ml-1 font-medium text-purple-500 hover:underline">
                  See Source Token Permissions
                </Link>
              )}
            </Callout>
          )}

          {check.scope?.estimated && (
            <p className="text-[12px] text-gray-500">
              That figure comes from a sample rather than a full count, so treat it as an estimate.
            </p>
          )}

          {check.evidence && <EvidencePanel evidence={check.evidence} title="How this was checked" />}

          {check.fix && <PreflightFix check={check} onFixed={onFixed} />}
        </div>
      )}
    </div>
  );
}

/**
 * The inline fix for a blocking check. Same shape as the run-time FixBox, but it
 * writes to the configuration rather than to a re-run — at this point nothing has
 * been migrated, so there is nothing to re-run yet.
 */
function PreflightFix({ check, onFixed }: { check: PreflightCheck; onFixed: () => void }) {
  const [token, setToken] = useState('');
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const fix = check.fix!;

  async function saveAndRecheck() {
    setSaving(true);
    setFailure(null);
    try {
      const current = await getConfiguration();
      await configure({
        source_host: current.source_host ?? '',
        target_host: current.target_host ?? '',
        dev_mode: current.dev_mode,
        migrate_workplace_name: current.migrate_workplace_name,
        // Only the field the fix names is replaced. Everything else is left as
        // the server already holds it — re-sending a token we never had would
        // clear it.
        ...(fix.field === 'source_token' ? { source_token: token.trim() } : {}),
        ...(fix.field === 'target_token' ? { target_token: token.trim() } : {}),
      });
      onFixed();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'That token was not accepted');
    } finally {
      setSaving(false);
    }
  }

  if (fix.kind !== 'token') {
    return (
      <div className="rounded-md border border-purple-200 bg-purple-50 px-4 py-3.5">
        <Eyebrow className="mb-1.5 text-purple-600">Fix before you start</Eyebrow>
        {fix.hint && <p className="mb-2.5 text-[13px] text-gray-700">{fix.hint}</p>}
        <Button onClick={onFixed}>
          <RotateCw size={13} /> {fix.label}
        </Button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2.5 rounded-md border border-purple-200 bg-purple-50 px-4 py-3.5">
      <Eyebrow className="text-purple-600">Fix before you start</Eyebrow>
      <TextField
        id="preflight-fix-token"
        mono
        name="preflight-fix-token"
        label="Replacement token"
        type="password"
        placeholder="dp.sa.…"
        value={token}
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => setToken(e.target.value)}
        hint={fix.hint}
      />
      {failure && (
        <p className="text-[11.5px] text-red-600">{failure}</p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button onClick={saveAndRecheck} disabled={saving || token.trim().length === 0}>
          <RotateCw size={13} /> {saving ? 'Saving…' : fix.label}
        </Button>
      </div>
      {fix.blocking && (
        <p className="text-[11.5px] text-gray-500">
          Continuing without fixing this means those items fail during the run. You can still fix
          them there.
        </p>
      )}
    </div>
  );
}

function StatusPill({ status }: { status: PreflightCheck['status'] }) {
  switch (status) {
    case 'passed':
      return <Pill tone="done">Passed</Pill>;
    case 'warning':
      return <Pill tone="warning">Warning</Pill>;
    case 'failed':
      return <Pill tone="error">Blocking</Pill>;
    case 'running':
      return <Pill tone="running">Checking</Pill>;
    case 'skipped':
      return <Pill tone="skipped">Skipped</Pill>;
    default:
      return null;
  }
}
