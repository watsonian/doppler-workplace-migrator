import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowRight, Crosshair, Download, FileText, KeyRound, Lock, ShieldAlert } from 'lucide-react';
import {
  Block,
  BlockHeader,
  Button,
  Callout,
  Checkbox,
  Eyebrow,
  FooterBar,
  Hint,
  PageHeading,
  Pill,
  TextField,
} from '@/components/ui/primitives';
import DiffRollup from '@/components/diff/DiffRollup';
import {
  errorsExportURL,
  finalReportJSONURL,
  finalReportURL,
  getErrors,
  planCutover,
  setStandby,
  startCutover,
  subscribeSSE,
  tokenWorklistURL,
} from '@/api/client';
import type { CutoverPlan, CutoverResult, TokenGate } from '@/api/client';
import { isInFlight, useStandby } from '@/hooks/useStandby';

/**
 * Cutover — the trusted-IP mirror, run once as the last step, and where the
 * record of the whole migration is downloaded.
 */
export default function CutoverPage() {
  const navigate = useNavigate();
  const { standby } = useStandby();
  const inFlight = isInFlight(standby);

  const [plan, setPlan] = useState<CutoverPlan | null>(null);
  const [cidr, setCidr] = useState('');
  const [selfTrust, setSelfTrust] = useState(true);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<CutoverResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [errorCount, setErrorCount] = useState(0);
  const [finishing, setFinishing] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [tokenGate, setTokenGate] = useState<TokenGate | null>(null);
  const [partialOnly, setPartialOnly] = useState(false);

  const unsubRef = useRef<(() => void) | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const resp = await planCutover('');
      // Go encodes a list nothing was appended to as null. The server now sends
      // [] for these, but a null here used to blank the whole page, so it is
      // not left to the server alone.
      setPlan({
        ...resp.plan,
        trusted_ips: resp.plan.trusted_ips ?? [],
        matching: resp.plan.matching ?? [],
        unreadable: resp.plan.unreadable ?? [],
      });
      setTokenGate(resp.token_gate ?? null);
      setPartialOnly(Boolean(resp.partial_only));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not work out what cutover would do');
    }
  }, []);

  useEffect(() => {
    void load();
    // Only to decide whether an errors CSV is worth offering.
    getErrors()
      .then((e) => setErrorCount(e.total))
      .catch(() => setErrorCount(0));
    return () => {
      unsubRef.current?.();
      unsubRef.current = null;
    };
  }, [load]);

  const onEvent = useCallback((type: string, data: unknown) => {
    if (type === 'cutover_progress') {
      const p = data as { item?: string; error?: string };
      setNotice(p.error ? `${p.item}: ${p.error}` : (p.item ?? null));
    } else if (type === 'cutover_complete') {
      const payload = data as { status: string; error?: string; result?: CutoverResult };
      setRunning(false);
      setResult(payload.result ?? null);
      if (payload.status === 'failed') {
        setError(payload.error || 'The cutover did not finish');
      }
    }
  }, []);

  async function run() {
    setRunning(true);
    setError(null);
    setNotice(null);

    const sse = subscribeSSE(onEvent);
    unsubRef.current = sse.unsubscribe;

    try {
      await startCutover(selfTrust ? cidr.trim() : '');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'The cutover could not be started');
      setRunning(false);
    }
  }

  const needsSelfTrust = (plan?.closing_count ?? 0) > 0;
  const tokenBlocked = Boolean(tokenGate?.blocked);
  // Nothing is known until the plan loads — the token gate and the partial-run
  // flag come with it — so until then the step is not ready. Enabled first and
  // disabled a moment later, the button invited a quick click on a cutover the
  // page was about to forbid.
  const ready =
    plan !== null &&
    (!needsSelfTrust || !selfTrust || cidr.trim().length > 0) &&
    !tokenBlocked &&
    !partialOnly;
  const undecided = (tokenGate?.counts.pending ?? 0) + (tokenGate?.counts.deferred ?? 0);
  const setAside = tokenGate?.counts.deferred ?? 0;
  const nothingToDo = plan !== null && plan.trusted_ips.length === 0;
  const canFinish = Boolean(standby?.full_done && !standby.cutover_at) && !partialOnly;

  async function finish() {
    setFinishing(true);
    setError(null);
    try {
      await setStandby('finish');
      navigate('/complete');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not finish the migration');
      setFinishing(false);
    }
  }

  return (
    <div className="mx-auto w-full max-w-[720px] px-6 py-6">
      <PageHeading
        title="Cut Over"
        subtitle="Applies the source’s trusted IP ranges to the target, as the last step of the migration."
      />

      <div className="mt-5 flex flex-col gap-3.5">
        {error && <Callout tone="error">{error}</Callout>}
        {notice && running && <Callout tone="info">{notice}</Callout>}

        {result && <CutoverOutcome result={result} />}

        {!result && nothingToDo && (
          <Callout tone="success">
            Nothing to cut over. Every config’s trusted IP ranges on the target already match the
            source.
            {canFinish && ' Finish the migration to record that it is done; nothing on the target changes.'}
          </Callout>
        )}

        {!result && !nothingToDo && (
          <Callout tone="error">
            Run cutover once, after changes on the source have stopped. The migrator can’t undo
            these changes.
          </Callout>
        )}

        {/* After a partial the target holds a handful of projects. Mirroring
            trusted IPs there would lock down a workplace that is mostly not
            there yet. */}
        {!result && partialOnly && (
          <Callout tone="error">
            <span className="font-semibold text-gray-900">
              Cutover needs a full migration first.
            </span>{' '}
            Only some projects have been migrated so far. Cutover sets trusted IPs across the whole
            target workplace, so it’s unavailable until everything has been migrated.
            <span className="mt-2 block">
              <Button variant="secondary" onClick={() => navigate('/scope')}>
                Choose A Full Migration
              </Button>
            </span>
          </Callout>
        )}

        {/* Cutover removes the config write access that issuing a service token
            needs, and nothing can restore it — a second cutover finds the ranges
            already mirrored and does nothing. So an undecided token here is a
            decision that could never be made again, and the run is refused
            rather than warned about. Skipping counts as deciding, so there is
            always a way through. */}
        {!result && tokenBlocked && (
          <Callout tone="error">
            <span className="font-semibold text-gray-900">
              {undecided} service {undecided === 1 ? 'token is' : 'tokens are'} still undecided
              {setAside > 0 && ` (${setAside === 1 ? 'one' : setAside} set aside for later)`}.
            </span>{' '}
            This step removes the write access that issuing one needs, and it cannot be restored
            afterwards. Issue or skip {undecided === 1 ? 'it' : 'them'} first — skipping counts
            {setAside > 0 && ', and a token set aside for later has to be issued or skipped too'}.
            <span className="mt-2 block">
              <Button variant="secondary" onClick={() => navigate('/service-tokens')}>
                <KeyRound size={13} /> Open Service Tokens
              </Button>
            </span>
          </Callout>
        )}

        {!result && tokenGate && !tokenGate.blocked && tokenGate.counts.total > 0 && (
          <Callout tone="success">
            All {tokenGate.counts.total} service tokens are handled — {tokenGate.counts.issued}{' '}
            issued on the target, {tokenGate.counts.to_recreate + tokenGate.counts.not_needed}{' '}
            skipped. The worklist stays downloadable after this runs.
          </Callout>
        )}

        {plan && !result && plan.unreadable.length > 0 && (
          <Callout tone="warning">
            <span className="font-semibold text-gray-900">
              The trusted IP ranges of {plan.unreadable.length}{' '}
              {plan.unreadable.length === 1 ? 'config' : 'configs'} could not be read from the
              target.
            </span>{' '}
            Cutover leaves {plan.unreadable.length === 1 ? 'it' : 'them'} alone:{' '}
            <span className="font-mono text-[12px]">{plan.unreadable.join(', ')}</span>. Check{' '}
            {plan.unreadable.length === 1 ? 'its' : 'their'} ranges in the target dashboard.
          </Callout>
        )}

        {plan && !result && plan.trusted_ips.length > 0 && (
          <Block>
            <BlockHeader title="1 · Apply Trusted IP Ranges">
              <Pill tone="warning">Irreversible</Pill>
            </BlockHeader>

            <div className="flex flex-col gap-3 p-3.5">
              <p className="text-[13px] text-gray-700">
                Mirroring {plan.trusted_ips.length}{' '}
                {plan.trusted_ips.length === 1 ? 'config' : 'configs'} so their trusted lists match
                the source exactly. The other {plan.already_matching.toLocaleString()} already match
                and are left alone.
                {needsSelfTrust && (
                  <>
                    {' '}
                    Once the default comes off, this migrator can no longer read or write{' '}
                    {plan.closing_count} of them — the restriction covers the trusted-IP endpoint
                    too, so it cannot lift it again either.
                  </>
                )}
              </p>

              <Block>
                <DiffRollup
                  label="Configs to mirror"
                  detail="Their trusted lists do not match the source."
                  count={plan.trusted_ips.length}
                  defaultOpen
                  columns={[
                    { label: 'Config', width: '150px', mono: true, muted: true },
                    { label: 'Add', width: '1fr', mono: true },
                    { label: 'Remove', width: '1fr', mono: true },
                  ]}
                  rows={plan.trusted_ips.map((w) => [
                    w.scope,
                    (w.add ?? []).join(', ') || '—',
                    (w.remove ?? []).join(', ') || '—',
                  ])}
                />
                <DiffRollup
                  label="Configs already matching"
                  detail="Nothing to add or remove. No requests made."
                  count={plan.already_matching}
                  columns={[{ label: 'Config', width: '1fr', mono: true }]}
                  rows={plan.matching.map((scope) => [scope])}
                />
              </Block>

              {needsSelfTrust && (
                <div className="flex flex-col gap-2.5 rounded-md border border-purple-200 bg-purple-50 px-3.5 py-3">
                  <Eyebrow className="text-purple-600">Migrator access during cutover</Eyebrow>

                  <label className="flex cursor-pointer items-start gap-2.5 text-[13px] text-gray-700">
                    <Checkbox
                      className="mt-0.5"
                      checked={selfTrust}
                      onChange={(e) => setSelfTrust(e.target.checked)}
                    />
                    <span>
                      <b className="text-gray-900">
                        Add this migrator&rsquo;s address to those configs, then remove it at the end
                      </b>
                      <br />
                      <span className="text-xs text-gray-500">
                        The restrictions apply immediately, except for this machine. Step 2 removes
                        that exception.
                      </span>
                    </span>
                  </label>

                  {selfTrust && (
                    <>
                      <div className="flex items-end gap-2">
                        <span className="flex-1">
                          <TextField
                            id="migrator-cidr"
                            mono
                            label="This migrator's public address"
                            placeholder="203.0.113.47/32"
                            value={cidr}
                            spellCheck={false}
                            autoComplete="off"
                            onChange={(e) => setCidr(e.target.value)}
                          />
                        </span>
                        <Button
                          variant="secondary"
                          onClick={() => setNotice('Detection is not wired up — paste the address your egress uses.')}
                        >
                          <Crosshair size={13} /> Detect
                        </Button>
                      </div>
                      <Hint>
                        Doppler never reports the address it sees, so this cannot be filled in
                        automatically with certainty. Check it matches your egress before continuing.
                      </Hint>
                    </>
                  )}

                  {!selfTrust && (
                    <Callout tone="info" className="text-[12.5px]">
                      The ranges are applied as they are. The run will complete, and any later run
                      will get a 403 on those {plan.closing_count} configs.
                    </Callout>
                  )}
                </div>
              )}
            </div>
          </Block>
        )}

        {plan && !result && needsSelfTrust && selfTrust && cidr.trim() && (
          <Block>
            <BlockHeader title="2 · Remove Migrator IP">
              <Pill tone="running">Runs last</Pill>
            </BlockHeader>
            <div className="flex items-start gap-2.5 px-3.5 py-2.5 text-[13px]">
              <Lock size={14} className="mt-0.5 shrink-0 text-purple-500" />
              <span>
                Remove <span className="font-mono">{cidr.trim()}</span> from {plan.closing_count}{' '}
                {plan.closing_count === 1 ? 'config' : 'configs'}
                <br />
                <span className="text-xs text-gray-500">
                  The last thing the run does. Until it succeeds, the summary reports the migration
                  as unfinished.
                </span>
              </span>
            </div>
          </Block>
        )}

        {/* The record of the whole run, kept here because this is the last
            step: after it there is nowhere else to come back to for it. */}
        <Block>
          <BlockHeader title="Migration Report">
            {partialOnly ? (
              <Pill tone="waiting">Partial migration</Pill>
            ) : result || nothingToDo ? (
              <Pill tone="done">Final</Pill>
            ) : (
              <Pill tone="waiting">Cutover not run</Pill>
            )}
          </BlockHeader>
          <div className="flex flex-col gap-3 p-3.5">
            <p className="text-[13px] text-gray-700">
              Everything this migration did: the runs, each phase, the service tokens, cutover, any
              errors, and what is still to do by hand. It lists names and counts only — no secret or
              token values.
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="secondary"
                size="md"
                onClick={() => {
                  window.location.href = finalReportURL;
                }}
              >
                <FileText size={14} /> Download Report
              </Button>
              <Button
                variant="ghost"
                size="md"
                onClick={() => {
                  window.location.href = finalReportJSONURL;
                }}
              >
                <Download size={14} /> As JSON
              </Button>
              {errorCount > 0 && (
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
              <Button
                variant="ghost"
                size="md"
                onClick={() => {
                  window.location.href = tokenWorklistURL;
                }}
              >
                <Download size={14} /> Token Worklist CSV
              </Button>
            </div>
          </div>
        </Block>

        <FooterBar
          left={
            <Button variant="ghost" onClick={() => navigate(inFlight ? '/standby' : '/complete')} disabled={running}>
              Back
            </Button>
          }
          right={
            result ? (
              <Button size="md" onClick={() => navigate('/complete')}>
                Continue <ArrowRight size={14} />
              </Button>
            ) : nothingToDo && canFinish ? (
              // Nothing to mirror, so nothing on the target changes: this only
              // records that the migration is done, which ends the waiting.
              <Button size="md" onClick={finish} disabled={tokenBlocked || finishing}>
                {finishing ? 'Finishing…' : 'Finish Migration'} <ArrowRight size={14} />
              </Button>
            ) : (
              <Button variant="danger" size="md" onClick={run} disabled={!ready || running || nothingToDo}>
                {running ? 'Cutting over…' : 'Cut Over'} <ArrowRight size={14} />
              </Button>
            )
          }
        />
      </div>
    </div>
  );
}

/**
 * The outcome, which is not simply "done".
 *
 * A cutover that could not remove its own address has not finished, however well
 * the rest of it went — trusted IPs are bare CIDR strings with nowhere to put a
 * label, so one left behind is indistinguishable from one somebody added on
 * purpose. It has to be named, in full, until it is gone.
 */
function CutoverOutcome({ result }: { result: CutoverResult }) {
  const outstanding = result.self_trust_outstanding ?? [];

  if (outstanding.length === 0 && result.failed === 0) {
    return (
      <Callout tone="success">
        <span className="font-semibold text-gray-900">Cutover complete.</span>{' '}
        {result.configs_mirrored} configs mirrored, {result.ranges_added} ranges added,{' '}
        {result.ranges_removed} removed. This migrator no longer has access to the restricted
        configs, which is the intended end state.
      </Callout>
    );
  }

  return (
    <Callout tone="error">
      <div className="flex flex-col gap-1.5">
        <span className="flex items-center gap-2 font-semibold text-gray-900">
          <ShieldAlert size={14} className="text-red-500" /> Cutover did not finish
        </span>
        {outstanding.length > 0 && (
          <p>
            <span className="font-mono">{result.migrator_cidr}</span> is still trusted by{' '}
            {outstanding.length} {outstanding.length === 1 ? 'config' : 'configs'}. It grants access
            and there is nothing on it to say it was ours — remove it by hand if a re-run will not.
          </p>
        )}
        {outstanding.length > 0 && (
          <ul className="ml-4 list-disc font-mono text-[12px]">
            {outstanding.map((scope) => (
              <li key={scope}>{scope}</li>
            ))}
          </ul>
        )}
        {result.failed > 0 && <p>{result.failed} operations failed. They are listed in triage.</p>}
      </div>
    </Callout>
  );
}
