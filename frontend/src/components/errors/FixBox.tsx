import { useState } from 'react';
import { Gauge, KeyRound, RotateCw } from 'lucide-react';
import { Button, Eyebrow, Radio, TextField } from '@/components/ui/primitives';
import type { Remedy, RemedyKind, ResolveRequest } from '@/types/errors';

/**
 * The fix for a whole class of failures.
 *
 * The button label comes from the server and never carries a count — a label
 * baked with a number is wrong the moment the run records one more failure. The
 * count belongs in the sentence above it, which is re-rendered from live data.
 */

const REMEDY_ICONS: Partial<Record<RemedyKind, typeof KeyRound>> = {
  token: KeyRound,
  pace: Gauge,
  recheck: RotateCw,
};

/** Gaps offered for "re-run slower", in milliseconds between requests. */
const PACE_OPTIONS = [
  { ms: 100, label: 'Gentle — 100 ms' },
  { ms: 250, label: 'Slow — 250 ms' },
  { ms: 1000, label: 'Very slow — 1 s' },
];

export interface FixBoxProps {
  remedy: Remedy;
  /** How much this fix will touch, already worded, e.g. "214 configs · 6,412 secrets". */
  scopeSentence?: string;
  busy?: boolean;
  disabled?: boolean;
  onApply: (req: ResolveRequest) => void;
  onDismiss?: () => void;
  dismissLabel?: string;
}

export default function FixBox({
  remedy,
  scopeSentence,
  busy = false,
  disabled = false,
  onApply,
  onDismiss,
  dismissLabel,
}: FixBoxProps) {
  const [token, setToken] = useState('');
  const [paceMs, setPaceMs] = useState(PACE_OPTIONS[1].ms);
  const [choice, setChoice] = useState(remedy.choices?.[0]?.value ?? '');

  if (remedy.kind === 'none') {
    return (
      <div className="rounded-md border border-gray-75 bg-gray-25 px-4 py-3">
        <p className="text-[13px] text-gray-600">{remedy.detail}</p>
      </div>
    );
  }

  const Icon = REMEDY_ICONS[remedy.kind];
  const ready = remedy.kind !== 'token' || token.trim().length > 0;

  function apply() {
    const req: ResolveRequest = { remedy: remedy.kind };
    if (remedy.kind === 'token') req.token = token.trim();
    if (remedy.kind === 'pace') req.pace_ms = paceMs;
    if (remedy.kind === 'decision') req.choice = choice;
    onApply(req);
  }

  return (
    <div className="flex flex-col gap-2.5 rounded-md border border-purple-200 bg-purple-50 px-4 py-3.5">
      <Eyebrow className="text-purple-600">Fix the whole class</Eyebrow>

      <p className="text-[13px] leading-relaxed text-gray-700">
        {remedy.detail}
        {scopeSentence && <> {scopeSentence}</>}
      </p>

      {remedy.kind === 'token' && (
        <TextField
          id="fix-token"
          mono
          name="fix-token"
          label="Replacement token"
          type="password"
          placeholder="dp.sa.…"
          value={token}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setToken(e.target.value)}
          hint="Create a service account token with read access to the affected projects."
        />
      )}

      {remedy.kind === 'pace' && (
        <fieldset className="flex flex-col gap-1.5">
          <legend className="text-xs font-semibold text-gray-700">Gap between requests</legend>
          {/* A segmented control, so the input is hidden and the label carries the
              selected state. That hides the focus ring along with it — the ring
              lands on a clipped element — so the label borrows it instead. */}
          <div className="flex flex-wrap gap-2">
            {PACE_OPTIONS.map((option) => (
              <label
                key={option.ms}
                className={`flex cursor-pointer items-center gap-2 rounded-md border px-3 py-1.5 text-[12.5px] ${
                  paceMs === option.ms
                    ? 'border-purple-500 bg-gray-0 font-semibold text-purple-600'
                    : 'border-gray-100 bg-gray-0 text-gray-600'
                } has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-blue-500 has-[:focus-visible]:ring-offset-1`}
              >
                <input
                  type="radio"
                  name="fix-pace"
                  className="sr-only"
                  checked={paceMs === option.ms}
                  onChange={() => setPaceMs(option.ms)}
                />
                {option.label}
              </label>
            ))}
          </div>
        </fieldset>
      )}

      {remedy.kind === 'decision' && remedy.choices && (
        <fieldset className="flex flex-col gap-1.5">
          <legend className="text-xs font-semibold text-gray-700">What should happen</legend>
          {remedy.choices.map((option) => (
            <label
              key={option.value}
              className={`flex cursor-pointer items-start gap-2.5 rounded-md border px-3 py-2 ${
                choice === option.value ? 'border-purple-500 bg-gray-0' : 'border-gray-100 bg-gray-0'
              }`}
            >
              <Radio
                name="fix-choice"
                className="mt-0.5"
                checked={choice === option.value}
                onChange={() => setChoice(option.value)}
              />
              <span className="min-w-0">
                <span className="block text-[13px] font-semibold text-gray-900">{option.label}</span>
                <span className="block text-[12px] text-gray-500">{option.description}</span>
              </span>
            </label>
          ))}
        </fieldset>
      )}

      <div className="flex flex-wrap gap-2">
        <Button onClick={apply} disabled={disabled || busy || !ready}>
          {Icon && <Icon size={14} />}
          {busy ? 'Working…' : remedy.label}
        </Button>
        {onDismiss && (
          <Button variant="secondary" onClick={onDismiss} disabled={busy}>
            {dismissLabel ?? 'Skip And Note In Summary'}
          </Button>
        )}
      </div>
    </div>
  );
}
