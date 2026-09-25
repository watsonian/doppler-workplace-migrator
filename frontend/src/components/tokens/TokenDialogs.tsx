import { useState, type ReactNode } from 'react';
import { AlertTriangle, ArrowRightCircle, Ban, Check, Copy, Eye, EyeOff, KeyRound, RotateCw } from 'lucide-react';
import { Button, Callout, Modal, Radio } from '@/components/ui/primitives';
import type { IssuedToken, TokenRecord } from '@/api/client';
import { tokenPlacement } from '@/lib/tokenPlacement';

/**
 * The dialogs around a value that exists exactly once.
 *
 * None of these lift the value anywhere it could outlive the dialog: it arrives
 * as a prop, lives in the DOM while the dialog is open, and goes when the parent
 * drops it. No storage, no lifted state, no logging.
 */

/** What stands in for the value while it is hidden. */
const MASK = '\u2022'.repeat(96);

function formatDate(value?: string | null): string {
  if (!value) return 'Never';
  const d = new Date(value);
  return Number.isNaN(d.getTime())
    ? value
    : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

function formatTime(value?: string | null): string {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleTimeString(undefined, { hour12: false });
}

/* ── Reveal ─────────────────────────────────────────────────────────────── */

export function RevealDialog({ token, onClose }: { token: IssuedToken; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [revealed, setRevealed] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(token.value);
      setCopied(true);
    } catch {
      // A blocked clipboard is not a failure worth a dialog of its own — the
      // value is on screen and selectable, which is the fallback.
      setCopied(false);
    }
  }

  function attemptClose() {
    if (copied) {
      onClose();
      return;
    }
    // The one interruption worth spending: closing here destroys the only copy,
    // and nothing later can tell you that is what happened.
    setConfirming(true);
  }

  if (confirming) {
    return (
      <Modal
        title="Close Without Copying?"
        subtitle="The value has not been copied. It cannot be shown again."
        icon={<AlertTriangle size={15} />}
        tone="warning"
        width={440}
        onClose={() => setConfirming(false)}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirming(false)}>
              Back To The Value
            </Button>
            <Button variant="danger" onClick={onClose}>
              Close Anyway
            </Button>
          </>
        }
      >
        <p className="text-[13px] text-gray-600">
          The token stays on the target and keeps working for anything that already has it — but
          nothing does yet. You would need to reroll to get a value you can use.
        </p>
      </Modal>
    );
  }

  return (
    <Modal
      title={copied ? 'Copied To The Clipboard' : 'Copy This Token Now'}
      subtitle={
        copied
          ? 'Still the only copy. Closing this dialog discards it.'
          : 'It is shown this once. The target never returns it again, and neither can the migrator.'
      }
      icon={copied ? <Check size={15} /> : <KeyRound size={15} />}
      tone={copied ? 'success' : 'warning'}
      dismissible={false}
      onClose={attemptClose}
      footer={
        <>
          <span className="mr-auto text-xs text-gray-500">
            {token.replaced_at
              ? `Rerolled at ${formatTime(token.issued_at)}`
              : `Created on the target at ${formatTime(token.issued_at)}`}
          </span>
          <Button onClick={attemptClose}>{copied ? 'Done' : 'I Have Copied It'}</Button>
        </>
      }
    >
      <dl className="divide-y divide-gray-75 rounded-md border border-gray-75 text-[13px]">
        <DetailRow label="Token">{token.name}</DetailRow>
        <DetailRow label="Placement">{token.scope}</DetailRow>
        {token.access && <DetailRow label="Access">{token.access}</DetailRow>}
        <DetailRow label="Expires">{formatDate(token.expires_at)}</DetailRow>
      </dl>

      <div className="flex flex-col gap-1.5">
        <span className="block text-xs leading-normal text-gray-975">
          Value
        </span>
        <div className="flex items-center gap-2">
          <div
            className={`flex h-10 min-w-0 flex-1 items-center rounded-md border pl-3 pr-1 transition-colors ${
              copied ? 'border-green-500 bg-green-50' : 'border-gray-75 bg-gray-25'
            }`}
          >
            {/* Hidden until asked for: the dialog is often open on a shared screen
                or in a call, and copying does not need the value visible. The
                mask is a fixed string, longer than the field is wide, so it
                fills the field whatever its width and says nothing about the
                token's length. One line either way: a token broken across two
                reads as two things, and a stray newline is easy to paste. */}
            <code
              aria-label={revealed ? 'Token value' : 'Token value, hidden'}
              className={`min-w-0 flex-1 whitespace-nowrap font-mono text-[13px] leading-none text-gray-975 [mask-image:linear-gradient(to_right,black_calc(100%-20px),transparent)] ${
                revealed
                  ? 'overflow-x-auto py-2 pr-5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden'
                  : 'select-none overflow-hidden tracking-[0.12em] text-gray-600'
              }`}
            >
              {revealed ? token.value : MASK}
            </code>
            <button
              type="button"
              onClick={() => setRevealed((v) => !v)}
              aria-pressed={revealed}
              aria-label={revealed ? 'Hide token' : 'Show token'}
              title={revealed ? 'Hide token' : 'Show token'}
              className="ml-1 grid h-8 w-8 shrink-0 place-items-center rounded-md text-gray-500 transition-colors hover:bg-gray-75 hover:text-gray-800"
            >
              {revealed ? <EyeOff size={15} /> : <Eye size={15} />}
            </button>
          </div>
          <Button
            variant="secondary"
            size="md"
            onClick={copy}
            className={`min-w-[92px] px-3.5 ${copied ? 'border-green-500 text-green-500 hover:border-green-500' : ''}`}
          >
            {copied ? <Check size={14} /> : <Copy size={14} />}
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </div>
      </div>

      {token.expiry_shortened && (
        <Callout tone="warning">
          The source expiry is further out than the target allows, so this was issued at the
          one-year maximum instead.
        </Callout>
      )}

      {token.replaced_at ? (
        <Callout tone="error">
          The previous value is now invalid. Anything still holding it is failing authentication
          until this one replaces it.
        </Callout>
      ) : (
        <Callout tone="warning">
          Paste it into whatever used the old token before you close this. If you close without
          copying, the token still exists on the target but its value is gone — reroll to replace
          it with one you can read.
        </Callout>
      )}
    </Modal>
  );
}

/** One line of the token's details: a label and its value. */
function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[88px_1fr] items-baseline gap-3 px-3 py-1.5">
      <dt className="text-xs text-gray-600">{label}</dt>
      <dd className="min-w-0 break-all font-mono text-[12.5px] text-gray-975">{children}</dd>
    </div>
  );
}

/* ── Skip ───────────────────────────────────────────────────────────────── */

export function SkipDialog({
  record,
  busy,
  onSkip,
  onClose,
}: {
  record: TokenRecord;
  busy: boolean;
  onSkip: (notNeeded: boolean) => void;
  onClose: () => void;
}) {
  const [notNeeded, setNotNeeded] = useState(false);

  return (
    <Modal
      title="Skip This Token"
      subtitle={`${record.name} · ${tokenPlacement(record)}`}
      icon={<ArrowRightCircle size={15} />}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => onSkip(notNeeded)} disabled={busy}>
            {busy ? 'Working…' : 'Skip Token'}
          </Button>
        </>
      }
    >
      <SkipChoice
        checked={!notNeeded}
        onSelect={() => setNotNeeded(false)}
        title="Recreate it by hand"
        detail="Goes on the exported worklist with everything needed to rebuild it: placement, access and original expiry."
      />
      <SkipChoice
        checked={notNeeded}
        onSelect={() => setNotNeeded(true)}
        title="Not needed"
        detail="Retired or replaced. Kept off the worklist, and recorded as a deliberate drop rather than an oversight."
      />
    </Modal>
  );
}

function SkipChoice({
  checked,
  onSelect,
  title,
  detail,
}: {
  checked: boolean;
  onSelect: () => void;
  title: string;
  detail: string;
}) {
  return (
    <label
      className={`flex cursor-pointer items-start gap-2.5 rounded-md border p-3 ${
        checked ? 'border-purple-500 bg-purple-50' : 'border-gray-100'
      }`}
    >
      <Radio name="skip-reason" checked={checked} onChange={onSelect} className="mt-0.5" />
      <span className="min-w-0">
        <span className="block text-[13px] font-semibold text-gray-900">{title}</span>
        <span className="mt-0.5 block text-xs text-gray-500">{detail}</span>
      </span>
    </label>
  );
}

/* ── Skip all ───────────────────────────────────────────────────────────── */

export function SkipAllDialog({
  pending,
  busy,
  onConfirm,
  onClose,
}: {
  pending: number;
  busy: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal
      title="Skip All Remaining?"
      subtitle={`${pending} ${pending === 1 ? 'token is' : 'tokens are'} still undecided.`}
      icon={<ArrowRightCircle size={15} />}
      width={500}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={onConfirm} disabled={busy}>
            {busy ? 'Working…' : 'Skip All Remaining'}
          </Button>
        </>
      }
    >
      <p className="text-[13px] text-gray-600">
        They all go onto the worklist to recreate by hand. Nothing is created on the target and
        nothing already issued is affected. You can undo any of them individually afterwards.
      </p>
      <Callout tone="info">
        The worklist downloads as CSV from this page, and stays available after cutover.
      </Callout>
    </Modal>
  );
}

/* ── Reroll ─────────────────────────────────────────────────────────────── */

export function RerollDialog({
  record,
  busy,
  onConfirm,
  onClose,
}: {
  record: TokenRecord;
  busy: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const scope = tokenPlacement(record);
  const earlierRun = record.preexisting_on_target;

  return (
    <Modal
      title="Reroll This Token?"
      subtitle={`${record.name} · ${scope}`}
      icon={<RotateCw size={15} />}
      tone="error"
      width={480}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="danger" onClick={onConfirm} disabled={busy}>
            <RotateCw size={13} /> {busy ? 'Working…' : 'Reroll'}
          </Button>
        </>
      }
    >
      <p className="text-[13px] text-gray-600">
        {record.issued_at
          ? `The token issued at ${formatTime(record.issued_at)} is deleted from the target and a new one created with the same name, access and placement.`
          : 'The existing token is deleted from the target and a new one created with the same name, access and placement.'}{' '}
        Anything already using the old value stops working the moment this runs.
      </p>

      {earlierRun && (
        <Callout tone="warning">
          This migrator did not issue this token in this session — it was found on the target under
          the same name. If it is already deployed somewhere, rerolling breaks that.
        </Callout>
      )}

      <Callout tone="success">
        The token on the source workplace is not touched. Only the copy on the target is replaced.
      </Callout>
    </Modal>
  );
}

/* ── Failure ────────────────────────────────────────────────────────────── */

export function TokenFailureDialog({
  record,
  message,
  status,
  busy,
  onSkip,
  onClose,
}: {
  record: TokenRecord;
  message: string;
  status?: number;
  busy: boolean;
  onSkip: () => void;
  onClose: () => void;
}) {
  const scope = tokenPlacement(record);
  const gated = status === 403;

  return (
    <Modal
      title={gated ? 'Blocked By Trusted IPs' : 'The Token Was Not Issued'}
      subtitle={`${status ?? 'Failed'} · ${scope}`}
      icon={<Ban size={15} />}
      tone="error"
      width={560}
      onClose={onClose}
      footer={
        <>
          <span className="mr-auto text-xs text-gray-500">Nothing was created.</span>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            Close
          </Button>
          <Button onClick={onSkip} disabled={busy}>
            <ArrowRightCircle size={13} /> Skip And Add To Worklist
          </Button>
        </>
      }
    >
      <p className="text-[13px] text-gray-600">{message}</p>

      {gated && (
        <Callout tone="info">
          The migrator cannot let itself back in: a second cutover finds the ranges already mirrored
          and does nothing. Recreate this token in the target dashboard, or add the migrator's
          address to the config's trusted IPs there first.
        </Callout>
      )}
    </Modal>
  );
}
