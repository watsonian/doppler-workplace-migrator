import { useCallback, useEffect, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { isInFlight, useStandby } from '@/hooks/useStandby';
import { ArrowRight, Check, Lock } from 'lucide-react';
import {
  Block,
  BlockHeader,
  Button,
  Callout,
  Checkbox,
  FooterBar,
  Hint,
  PageHeading,
  Pill,
  Select,
  TextField,
} from '@/components/ui/primitives';
import { configure, getConfiguration } from '@/api/client';

const DOPPLER_CLOUD_HOST = 'https://api.doppler.com';

type InstanceType = 'cloud' | 'onprem';
type TokenKind = 'cli' | 'personal' | 'service_account' | 'unknown';

/** Mirrors the server's token classification, for immediate feedback as you type. */
function detectTokenKind(token: string): TokenKind | null {
  if (!token) return null;
  if (token.startsWith('dp.ct.')) return 'cli';
  if (token.startsWith('dp.pt.')) return 'personal';
  if (token.startsWith('dp.sa.')) return 'service_account';
  return 'unknown';
}

const TOKEN_LABELS: Record<TokenKind, string> = {
  cli: 'CLI token',
  personal: 'Personal token',
  service_account: 'Service account token',
  unknown: 'Unrecognized token',
};

/**
 * Only service account tokens can read secrets marked restricted. Saying so
 * while the token is being typed is much cheaper than saying it after two
 * hundred configs have failed.
 */
function readsRestricted(kind: TokenKind | null): boolean {
  return kind === 'service_account';
}

function isValidUrl(url: string): boolean {
  try {
    const parsed = new URL(url);
    return parsed.protocol === 'http:' || parsed.protocol === 'https:';
  } catch {
    return false;
  }
}

function inferInstanceType(host: string | undefined): InstanceType {
  if (!host || host === DOPPLER_CLOUD_HOST) return 'cloud';
  return 'onprem';
}

export default function ConfigurationPage() {
  const navigate = useNavigate();

  const [sourceInstance, setSourceInstance] = useState<InstanceType>('cloud');
  const [sourceHost, setSourceHost] = useState('');
  const [sourceToken, setSourceToken] = useState('');
  const [targetInstance, setTargetInstance] = useState<InstanceType>('onprem');
  const [targetHost, setTargetHost] = useState('');
  const [targetToken, setTargetToken] = useState('');
  const [devMode, setDevMode] = useState(false);
  const [devModeForced, setDevModeForced] = useState(false);
  const [migrateWorkplaceName, setMigrateWorkplaceName] = useState(false);
  const [stopOnNewErrorClass, setStopOnNewErrorClass] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [apiErrors, setApiErrors] = useState<string[]>([]);
  const [storedSourceHint, setStoredSourceHint] = useState('');
  const [storedTargetHint, setStoredTargetHint] = useState('');
  const [sourceFromEnv, setSourceFromEnv] = useState(false);
  const [targetFromEnv, setTargetFromEnv] = useState(false);

  // Token values never come back from the server — only a hint of which one is
  // loaded — so the inputs stay empty and an empty input on submit means "keep
  // the stored token".
  useEffect(() => {
    getConfiguration()
      .then((cfg) => {
        setDevModeForced(cfg.dev_mode_forced);
        if (cfg.dev_mode_forced) setDevMode(true);
        // Held tokens are reported before anything is configured too: the server
        // may have been started with SOURCE_DOPPLER_TOKEN / TARGET_DOPPLER_TOKEN,
        // and those should fill the fields on first load.
        setStoredSourceHint(cfg.has_source_token ? cfg.source_token_hint || '' : '');
        setStoredTargetHint(cfg.has_target_token ? cfg.target_token_hint || '' : '');
        setSourceFromEnv(Boolean(cfg.source_token_from_env));
        setTargetFromEnv(Boolean(cfg.target_token_from_env));
        if (!cfg.configured) return;
        setSourceInstance(inferInstanceType(cfg.source_host));
        setSourceHost(cfg.source_host === DOPPLER_CLOUD_HOST ? '' : cfg.source_host || '');
        setTargetInstance(inferInstanceType(cfg.target_host));
        setTargetHost(cfg.target_host === DOPPLER_CLOUD_HOST ? '' : cfg.target_host || '');
        setDevMode(cfg.dev_mode);
        setMigrateWorkplaceName(cfg.migrate_workplace_name);
        setStopOnNewErrorClass(cfg.stop_on_new_error_class ?? true);
      })
      .catch(() => {
        // A fresh start is a perfectly good outcome here.
      });
  }, []);

  // One migration at a time. After a restart the hosts are gone from memory,
  // but a migration waiting to cut over still owns the run history — so its
  // instances are filled in, and the server refuses any others.
  const { standby } = useStandby();
  const inFlight = isInFlight(standby);
  const returnTo = (useLocation().state as { returnTo?: string } | null)?.returnTo;
  useEffect(() => {
    if (!inFlight || !standby || standby.configured) return;
    if (standby.source_host) {
      setSourceInstance(inferInstanceType(standby.source_host));
      setSourceHost(standby.source_host === DOPPLER_CLOUD_HOST ? '' : standby.source_host);
    }
    if (standby.target_host) {
      setTargetInstance(inferInstanceType(standby.target_host));
      setTargetHost(standby.target_host === DOPPLER_CLOUD_HOST ? '' : standby.target_host);
    }
  }, [inFlight, standby]);

  const sourceKind = detectTokenKind(sourceToken);
  const targetKind = detectTokenKind(targetToken);

  const sourceTokenValid = sourceToken === '' || sourceKind !== 'unknown';
  const targetTokenValid = targetToken === '' || targetKind !== 'unknown';
  const sourceHostValid = sourceHost === '' || isValidUrl(sourceHost);
  const targetHostValid = targetHost === '' || isValidUrl(targetHost);

  const sourceHostReady = sourceInstance === 'cloud' || (sourceHost.length > 0 && sourceHostValid);
  const targetHostReady = targetInstance === 'cloud' || (targetHost.length > 0 && targetHostValid);
  const sourceTokenReady = sourceToken.length > 0 ? sourceTokenValid : storedSourceHint !== '';
  const targetTokenReady = targetToken.length > 0 ? targetTokenValid : storedTargetHint !== '';

  const sourceReady = sourceHostReady && sourceTokenReady;
  const formValid = sourceReady && (devMode || (targetHostReady && targetTokenReady));

  // The target card stays locked until the source is filled in, and says so in
  // the card rather than in a note somewhere below it — a disabled form with its
  // explanation off screen reads as a bug.
  const targetLocked = devMode || !sourceReady;

  const handleSubmit = useCallback(async () => {
    setSubmitting(true);
    setApiErrors([]);
    try {
      const resp = await configure({
        source_host: sourceInstance === 'cloud' ? DOPPLER_CLOUD_HOST : sourceHost,
        ...(sourceToken.length > 0 ? { source_token: sourceToken } : {}),
        target_host: targetInstance === 'cloud' ? DOPPLER_CLOUD_HOST : targetHost,
        ...(targetToken.length > 0 ? { target_token: targetToken } : {}),
        dev_mode: devMode,
        migrate_workplace_name: migrateWorkplaceName,
        stop_on_new_error_class: stopOnNewErrorClass,
      });
      // Sent here from the waiting page to enter the tokens again: the rest of
      // the migration is already done, so saving them goes straight back.
      if (resp.valid) navigate(returnTo ?? '/preflight');
      else setApiErrors(resp.errors || ['Validation failed']);
    } catch (err) {
      setApiErrors([err instanceof Error ? err.message : 'Configuration failed']);
    } finally {
      setSubmitting(false);
    }
  }, [
    sourceInstance,
    sourceHost,
    sourceToken,
    targetInstance,
    targetHost,
    targetToken,
    devMode,
    migrateWorkplaceName,
    stopOnNewErrorClass,
    navigate,
  ]);

  return (
    <div className="mx-auto w-full max-w-[720px] px-6 py-6">
      <PageHeading
        title="Configuration"
        subtitle="Point the migrator at the workplace you are moving from and the instance you are moving to."
      />

      <div className="mt-5 flex flex-col gap-3.5">
        {inFlight && standby?.target_host && (
          <Callout tone="info">
            <span className="font-semibold text-gray-900">Carrying on the migration to {standby.target_host}.</span>{' '}
            It is waiting to cut over, and only one migration can be in progress at a time. Other
            instances can be used once it is cut over.
          </Callout>
        )}
        {apiErrors.length > 0 && (
          <Callout tone="error">
            <ul className="ml-4 list-disc space-y-0.5">
              {apiErrors.map((e) => (
                <li key={e}>{e}</li>
              ))}
            </ul>
          </Callout>
        )}

        <div className="grid gap-3.5 md:grid-cols-2">
          <Block className="flex flex-col">
            <BlockHeader title="Source">
              <span className="flex items-center gap-2.5">
                {/* In the header rather than a hint below the field: the token has
                    to be made before this form can be filled in. */}
                <Link to="/permissions" className="text-[12.5px] font-medium text-purple-500 hover:underline">
                  How to create one
                </Link>
                {sourceReady ? (
                  <Pill tone="done">
                    <Check size={12} /> Ready
                  </Pill>
                ) : (
                  <Pill tone="skipped">Not verified</Pill>
                )}
              </span>
            </BlockHeader>
            <div className="flex flex-col gap-3 p-3.5">
              <InstanceSelect id="source-instance" value={sourceInstance} onChange={setSourceInstance} />
              {sourceInstance === 'onprem' && (
                <TextField
                  id="source-host"
                  mono
                  label="Host"
                  value={sourceHost}
                  onChange={(e) => setSourceHost(e.target.value)}
                  placeholder="https://doppler.internal.company.com"
                  invalid={!sourceHostValid}
                  hint={!sourceHostValid ? 'Must be a URL starting https://' : undefined}
                  hintTone={!sourceHostValid ? 'bad' : 'muted'}
                />
              )}
              <TokenInput
                id="source-token"
                value={sourceToken}
                onChange={setSourceToken}
                kind={sourceKind}
                storedHint={storedSourceHint}
                envVar={sourceFromEnv ? 'SOURCE_DOPPLER_TOKEN' : undefined}
                restrictedMatters
              />
            </div>
          </Block>

          <Block className={`flex flex-col ${targetLocked ? 'opacity-90' : ''}`}>
            <BlockHeader title="Target">
              {devMode ? (
                <Pill tone="warning">Dev Mode</Pill>
              ) : targetLocked ? (
                <Pill tone="skipped">
                  <Lock size={11} /> Locked
                </Pill>
              ) : targetTokenReady && targetHostReady ? (
                <Pill tone="done">
                  <Check size={12} /> Ready
                </Pill>
              ) : (
                <Pill tone="skipped">Not verified</Pill>
              )}
            </BlockHeader>

            {targetLocked ? (
              <div className="flex flex-1 flex-col items-center justify-center gap-2 px-4 py-8 text-center">
                <Lock size={17} className="text-gray-300" />
                <p className="max-w-[220px] text-[12.5px] text-gray-500">
                  {devMode
                    ? 'Dev Mode is on, so nothing is written and no target is needed. Turn it off below to migrate for real.'
                    : 'Fill in the source first. The target unlocks once the migrator knows what it is copying.'}
                </p>
              </div>
            ) : (
              <div className="flex flex-col gap-3 p-3.5">
                <InstanceSelect id="target-instance" value={targetInstance} onChange={setTargetInstance} />
                {targetInstance === 'onprem' && (
                  <TextField
                    id="target-host"
                    mono
                    label="Host"
                    value={targetHost}
                    onChange={(e) => setTargetHost(e.target.value)}
                    placeholder="https://doppler.internal.company.com"
                    invalid={!targetHostValid}
                    hint={!targetHostValid ? 'Must be a URL starting https://' : undefined}
                    hintTone={!targetHostValid ? 'bad' : 'muted'}
                  />
                )}
                <TokenInput
                  id="target-token"
                  value={targetToken}
                  onChange={setTargetToken}
                  kind={targetKind}
                  storedHint={storedTargetHint}
                  envVar={targetFromEnv ? 'TARGET_DOPPLER_TOKEN' : undefined}
                />
              </div>
            )}
          </Block>
        </div>

        <Block>
          <BlockHeader title="Options" />
          <ToggleRow
            id="opt-stop-on-new-class"
            checked={stopOnNewErrorClass}
            onChange={setStopOnNewErrorClass}
            title="Stop When A New Kind Of Error Appears"
            description="Pauses on the first failure of a kind so you can look at it. Repeats of a problem you have already seen keep going."
          />
          <ToggleRow
            id="opt-workplace-name"
            checked={migrateWorkplaceName}
            onChange={setMigrateWorkplaceName}
            title="Migrate The Workplace Name"
            description="Off by default. The target keeps its own name unless you ask for the source's."
          />
          <ToggleRow
            id="opt-dev-mode"
            checked={devMode}
            disabled={devModeForced}
            onChange={setDevMode}
            title="Dev Mode"
            description={
              devModeForced
                ? 'DEV_MODE is set in the server environment, pinning this session to a dry run.'
                : 'Read the source and plan the migration without writing anything to a target.'
            }
          />
        </Block>

        <FooterBar
          left={
            <Button variant="ghost" onClick={() => navigate('/')}>
              Back
            </Button>
          }
          right={
            <Button size="md" onClick={handleSubmit} disabled={!formValid || submitting}>
              {submitting ? 'Checking…' : 'Continue'} <ArrowRight size={14} />
            </Button>
          }
        />
      </div>
    </div>
  );
}

function InstanceSelect({
  id,
  value,
  onChange,
}: {
  id: string;
  value: InstanceType;
  onChange: (v: InstanceType) => void;
}) {
  return (
    <Select
      id={id}
      label="Instance"
      value={value}
      onChange={(e) => onChange(e.target.value as InstanceType)}
    >
      <option value="cloud">Doppler.com</option>
      <option value="onprem">Self-hosted</option>
    </Select>
  );
}

function TokenInput({
  id,
  value,
  onChange,
  kind,
  storedHint,
  envVar,
  restrictedMatters = false,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  kind: TokenKind | null;
  storedHint: string;
  /** The environment variable the held token came from, when it did. */
  envVar?: string;
  /** Source tokens decide whether restricted secrets can be read at all. */
  restrictedMatters?: boolean;
}) {
  const unknown = kind === 'unknown';
  const warnsRestricted = restrictedMatters && kind !== null && !unknown && !readsRestricted(kind);

  return (
    <div className="flex flex-col gap-1.5">
      <TextField
        id={id}
        label="API token"
        mono
        type="password"
        value={value}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => onChange(e.target.value)}
        placeholder={storedHint || 'dp.sa.… or dp.pt.…'}
        invalid={unknown}
        valid={kind !== null && !unknown}
      />
      {unknown ? (
        <Hint tone="bad">A Doppler token starts with dp.ct., dp.pt. or dp.sa.</Hint>
      ) : kind ? (
        <Hint tone={warnsRestricted ? 'muted' : 'good'}>
          <span className={warnsRestricted ? '' : 'font-semibold'}>{TOKEN_LABELS[kind]}</span>
          {warnsRestricted && (
            <>
              {' '}
              — cannot read secrets marked restricted. Pre-flight will tell you how many configs
              that would cost.
            </>
          )}
        </Hint>
      ) : storedHint ? (
        <Hint>
          {envVar ? (
            <>
              Using <span className="font-mono">{envVar}</span> from the environment{' '}
              (<span className="font-mono">{storedHint}</span>).
            </>
          ) : (
            <>
              Using the saved token <span className="font-mono">{storedHint}</span>.
            </>
          )}{' '}
          {/* The hint keeps the type prefix, so the restricted-secrets warning
              still applies to a token nobody typed. */}
          {restrictedMatters && !readsRestricted(detectTokenKind(storedHint)) &&
            'It cannot read secrets marked restricted — a service account token can. '}
          Type one to replace it.
        </Hint>
      ) : (
        <Hint>
          {restrictedMatters ? (
            <>
              A service account token is strongly preferred — it is the only kind that can read
              restricted secrets.
            </>
          ) : (
            'Needs workplace admin on the target.'
          )}
        </Hint>
      )}
    </div>
  );
}

function ToggleRow({
  id,
  checked,
  disabled,
  onChange,
  title,
  description,
}: {
  id: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
  title: string;
  description: string;
}) {
  return (
    <label
      htmlFor={id}
      className={`flex items-start gap-2.5 border-b border-gray-75 px-3.5 py-3 text-[13px] last:border-b-0 ${
        disabled ? 'opacity-60' : 'cursor-pointer'
      }`}
    >
      <Checkbox
        id={id}
        className="mt-0.5"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className="min-w-0">
        <span className="block font-semibold text-gray-900">{title}</span>
        <span className="block text-xs text-gray-500">{description}</span>
      </span>
    </label>
  );
}
