import { useEffect, useState } from 'react';
import { Hourglass } from 'lucide-react';
import { getRateLimits, subscribeSSE } from '@/api/client';
import type { RateLimitCategory, RateLimitEvent, RateWait } from '@/api/client';

/**
 * Shown whenever a rate limit is holding the migration up.
 *
 * A run paused by a rate limit looks exactly like a run that has hung: counters
 * stop and nothing says why. So this says which side is limiting, what kind of
 * request, and — the part that stops someone restarting a healthy run — when
 * it starts again, counting down to it.
 *
 * It lives in the layout rather than on any one page, because a pause can hit
 * the gather, the comparison, the run, a top-up or a token issue alike.
 */

const CATEGORY_LABELS: Record<RateLimitCategory, string> = {
  read: 'reads',
  secret_read: 'secret reads',
  write: 'writes',
};

/**
 * How long past its resume time a pause is still shown. Requests resume half a
 * second after the stated time, so the end event normally lands well inside it.
 */
const STALE_AFTER_MS = 5000;

function key(w: Pick<RateWait, 'side' | 'category'>): string {
  return `${w.side}/${w.category}`;
}

export default function RateLimitBanner() {
  const [waits, setWaits] = useState<Record<string, RateWait>>({});
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    let live = true;
    // A page opened mid-pause would otherwise show nothing until the next event.
    getRateLimits()
      .then((r) => {
        if (!live) return;
        // Merged under the live events, never over them: the stream connects
        // while this is in flight, and anything it has reported since is newer.
        const fetched = Object.fromEntries((r.waits ?? []).map((w) => [key(w), w]));
        setWaits((prev) => ({ ...fetched, ...prev }));
      })
      .catch(() => {});

    const sse = subscribeSSE((type, data) => {
      if (type !== 'rate_limit') return;
      const ev = data as RateLimitEvent;
      setWaits((prev) => {
        const next = { ...prev };
        if (ev.waiting) next[key(ev)] = ev;
        else delete next[key(ev)];
        return next;
      });
    });

    return () => {
      live = false;
      sse.unsubscribe();
    };
  }, []);

  // A pause well past its resume time is over, whether or not its end event
  // arrived: the fetch above can race the stream and restore one that had just
  // ended, which would otherwise sit at "Resuming now…" for good.
  const active = Object.values(waits)
    .filter((w) => new Date(w.resume_at).getTime() + STALE_AFTER_MS > now)
    .sort((a, b) => a.resume_at.localeCompare(b.resume_at));

  // Tick only while something is counting down.
  useEffect(() => {
    if (active.length === 0) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active.length]);

  if (active.length === 0) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      className="sticky top-0 z-30 border-b border-yellow-100 bg-yellow-50"
    >
      <div className="mx-auto flex max-w-[940px] flex-col gap-1 px-6 py-2.5">
        {active.map((w) => (
          <WaitLine key={key(w)} wait={w} now={now} />
        ))}
      </div>
    </div>
  );
}

function WaitLine({ wait, now }: { wait: RateWait; now: number }) {
  const resume = new Date(wait.resume_at);
  const left = Math.max(0, Math.ceil((resume.getTime() - now) / 1000));
  const side = wait.side === 'source' ? 'source' : 'target';

  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-gray-800">
      <Hourglass size={13} aria-hidden className="shrink-0 text-yellow-500" />
      <span>
        <b className="font-semibold text-gray-900">Paused by the {side}&rsquo;s rate limit</b> on{' '}
        {CATEGORY_LABELS[wait.category] ?? wait.category}
        {wait.refused ? ' — it asked the migrator to slow down.' : ' — the allowance for this window is used up.'}
      </span>
      <span className="ml-auto whitespace-nowrap font-mono text-[12.5px] tabular-nums text-gray-700">
        {left > 0 ? (
          <>
            Resumes at {formatClock(resume)} · in {formatLeft(left)}
          </>
        ) : (
          'Resuming now…'
        )}
      </span>
    </p>
  );
}

function formatClock(d: Date): string {
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function formatLeft(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return s === 0 ? `${m}m` : `${m}m ${s}s`;
}
