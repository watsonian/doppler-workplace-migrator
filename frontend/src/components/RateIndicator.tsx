import { useEffect, useRef, useState } from 'react';
import { getRateLimits, subscribeSSE } from '@/api/client';
import type { RateCategoryStatus, RateSideStatus, RateWait } from '@/api/client';

/**
 * Each token's standing against its rate limits, for the top bar: a dot per
 * side — green under 70% of the most-used limit, yellow to 99%, red once a
 * limit is spent — updated as requests are made, with when a spent limit
 * resets. Opens on hover or click to show each kind of request.
 *
 * Doppler reports the budget on every response; the server keeps the latest
 * per token and sends it at most every ¾ second, and at once when the colour
 * changes. A pause the limiter imposes (a 429, or the budget run out) counts
 * as red until it ends, with when requests resume.
 */

export type Side = 'source' | 'target';
type Level = '' | 'ok' | 'warn' | 'limited';

export interface SideState {
  status?: RateSideStatus;
  /** The latest-ending pause in progress for this side, if any. */
  resumeAt?: string;
}

const LEVEL_DOT: Record<Level, string> = {
  '': 'bg-gray-200',
  ok: 'bg-green-500',
  warn: 'bg-yellow-500',
  limited: 'bg-red-500',
};

const LEVEL_WORD: Record<Level, string> = {
  '': 'No requests yet',
  ok: 'Under 70% of its limits',
  warn: 'Nearing a limit',
  limited: 'Rate limited',
};

const CATEGORY_WORD: Record<string, string> = {
  read: 'Reads',
  secret_read: 'Secret reads',
  write: 'Writes',
};

/** The level to show: a pause in progress is red whatever the headers said. */
export function levelOf(state: SideState): Level {
  if (state.resumeAt) return 'limited';
  return state.status?.level ?? '';
}

/** Keeps both sides' state current from the API and the event stream. */
export function useRateState(): Record<Side, SideState> {
  const [status, setStatus] = useState<Partial<Record<Side, RateSideStatus>>>({});
  const [waits, setWaits] = useState<Record<string, RateWait>>({});
  const live = useRef(false);

  useEffect(() => {
    const applyStatus = (list: RateSideStatus[] | undefined) => {
      if (!list) return;
      setStatus(Object.fromEntries(list.map((s) => [s.side, s])));
    };

    getRateLimits()
      .then((r) => {
        // The stream is newer than a response that was in flight when it started.
        if (!live.current) applyStatus(r.status);
        setWaits((prev) => {
          const next = { ...prev };
          for (const w of r.waits ?? []) next[`${w.side}/${w.category}`] = w;
          return next;
        });
      })
      .catch(() => {});

    const sse = subscribeSSE((type, data) => {
      if (type === 'rate_status') {
        live.current = true;
        applyStatus(data as RateSideStatus[]);
      } else if (type === 'rate_limit') {
        const w = data as RateWait & { waiting: boolean };
        setWaits((prev) => {
          const next = { ...prev };
          const key = `${w.side}/${w.category}`;
          if (w.waiting) next[key] = w;
          else delete next[key];
          return next;
        });
      }
    });
    return () => sse.unsubscribe();
  }, []);

  // A pause whose end has passed without its closing event is over.
  const now = useNow(Object.keys(waits).length > 0 || Object.values(status).some((s) => s?.level === 'limited'));
  const out = {} as Record<Side, SideState>;
  for (const side of ['source', 'target'] as Side[]) {
    const pauses = Object.values(waits).filter((w) => w.side === side && new Date(w.resume_at).getTime() > now);
    const resumeAt = pauses.map((w) => w.resume_at).sort().at(-1);
    let st = status[side];
    // A spent window whose reset has passed is full again.
    if (st && st.level === 'limited' && st.reset_at && new Date(st.reset_at).getTime() <= now) {
      st = { ...st, level: 'ok' };
    }
    out[side] = { status: st, resumeAt };
  }
  return out;
}

/** The current time, ticking each second while something is counting down. */
function useNow(ticking: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [ticking]);
  return ticking ? now : Date.now();
}

/** The coloured dot before a host. */
export function RateDot({ state }: { state: SideState }) {
  return <span aria-hidden className={`inline-block h-2 w-2 shrink-0 rounded-full ${LEVEL_DOT[levelOf(state)]}`} />;
}

/** The figure after a host: how much of the limit is spent, or when it resets. */
export function RateFigure({ state }: { state: SideState }) {
  const level = levelOf(state);
  const reset = state.resumeAt ?? (level === 'limited' ? state.status?.reset_at : undefined);
  if (level === 'limited' && reset) {
    return <span className="font-mono text-[11.5px] text-red-600">resets {clock(reset)}</span>;
  }
  if (!level) return null;
  return <span className="font-mono text-[11.5px] tabular-nums text-gray-400">{percent(state.status?.used ?? 0)}</span>;
}

/** Dot and figure together, for a host shown elsewhere. */
export function RateBadge({ state }: { state: SideState }) {
  return (
    <>
      <RateDot state={state} />
      <RateFigure state={state} />
    </>
  );
}

/** The detail behind the dots: each side's kinds of request. */
export function RatePanel({ sides, hosts }: { sides: Record<Side, SideState>; hosts: Record<Side, string> }) {
  return (
    <div className="flex w-[300px] flex-col gap-3 p-3">
      <p className="text-[11.5px] text-gray-500">
        API rate limits, from the latest response for each token.
      </p>
      {(['source', 'target'] as Side[]).map((side) => {
        const state = sides[side];
        const level = levelOf(state);
        return (
          <div key={side} className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <span aria-hidden className={`inline-block h-2 w-2 rounded-full ${LEVEL_DOT[level]}`} />
              <b className="text-[12.5px] font-semibold capitalize text-gray-900">{side}</b>
              <span className="min-w-0 truncate font-mono text-[11px] text-gray-500">{hosts[side] || '—'}</span>
            </div>
            <p className={`text-[12px] ${level === 'limited' ? 'font-medium text-red-600' : 'text-gray-600'}`}>
              {state.resumeAt
                ? `Rate limited — requests resume at ${clock(state.resumeAt)} (${untilText(state.resumeAt)})`
                : level === 'limited' && state.status?.reset_at
                  ? `Rate limited — resets at ${clock(state.status.reset_at)} (${untilText(state.status.reset_at)})`
                  : LEVEL_WORD[level]}
            </p>
            {(state.status?.categories ?? []).length > 0 && (
              <ul className="flex flex-col gap-0.5">
                {state.status!.categories.map((c) => (
                  <CategoryRow key={c.category} c={c} />
                ))}
              </ul>
            )}
          </div>
        );
      })}
    </div>
  );
}

function CategoryRow({ c }: { c: RateCategoryStatus }) {
  const used = c.limit - c.remaining;
  const tone = c.used >= 1 ? 'bg-red-500' : c.used >= 0.7 ? 'bg-yellow-500' : 'bg-green-500';
  return (
    <li className="grid grid-cols-[84px_1fr_auto] items-center gap-2 text-[11.5px] text-gray-600">
      <span>{CATEGORY_WORD[c.category] ?? c.category}</span>
      <span className="h-1 overflow-hidden rounded-full bg-gray-75">
        <span className={`block h-full ${tone}`} style={{ width: `${Math.min(100, c.used * 100)}%` }} />
      </span>
      <span className="font-mono tabular-nums text-gray-500">
        {used.toLocaleString()}/{c.limit.toLocaleString()}
        {c.used >= 1 && c.reset_at && <span className="text-red-600"> · resets {clock(c.reset_at)}</span>}
      </span>
    </li>
  );
}

function percent(used: number): string {
  return `${Math.round(Math.min(1, Math.max(0, used)) * 100)}%`;
}

function clock(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleTimeString(undefined, { hour12: false });
}

function untilText(iso: string): string {
  const s = Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 1000));
  if (s < 60) return `in ${s}s`;
  const m = Math.floor(s / 60);
  return `in ${m}m ${s % 60}s`;
}
