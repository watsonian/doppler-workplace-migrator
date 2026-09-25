import { useEffect, useState } from 'react';
import { Filter, Layers, RotateCw } from 'lucide-react';
import { Button } from '@/components/ui/primitives';
import { getScope } from '@/api/client';
import type { ScopeMode } from '@/api/client';
import { formatWhen } from '@/lib/when';

/**
 * The run's scope, carried across the top of Review, Migrate and the summary.
 *
 * A partial run looks like any other run once it starts, so without this the
 * only place saying "three of ninety-two" would be a page left behind. Three
 * variants: a top-up, while waiting to cut over; a partial run; and a full run
 * finishing an earlier partial. A plain full run with nothing behind it needs
 * no band at all.
 */

export interface ScopeBandData {
  mode: ScopeMode;
  projects: string[];
  total: number;
  previouslyMigrated: number;
  previouslyMigratedAt?: string | null;
  /** Set when this run is a top-up: which one, and since when. */
  topUp?: { number: number; since?: string | null } | null;
}

/** Most names shown before the band summarises the rest as a count. */
const SHOWN = 3;

export function ScopeBand({
  scope,
  onChange,
  onBackToWaiting,
}: {
  scope: ScopeBandData | null;
  onChange?: () => void;
  /** Offered on a top-up in place of Change: a top-up's scope is always Full. */
  onBackToWaiting?: () => void;
}) {
  if (!scope) return null;

  if (scope.topUp && scope.mode === 'full') {
    return (
      <div
        role="status"
        className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-purple-100 bg-purple-50 px-3.5 py-2.5 text-[13px] text-gray-800"
      >
        <RotateCw size={12} aria-hidden className="shrink-0 text-purple-500" />
        <span className="min-w-0">
          <b className="font-semibold text-purple-600">Top-up {scope.topUp.number}</b>
          {scope.topUp.since ? ` · changes on the source since ${formatWhen(scope.topUp.since)}` : ''}
        </span>
        {onBackToWaiting && (
          <Button variant="ghost" onClick={onBackToWaiting} className="ml-auto">
            Back To Waiting
          </Button>
        )}
      </div>
    );
  }

  if (scope.mode === 'partial') {
    const names = scope.projects.slice(0, SHOWN).join(', ');
    const more = scope.projects.length - SHOWN;
    return (
      <div
        role="status"
        className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-purple-100 bg-purple-50 px-3.5 py-2.5 text-[13px] text-gray-800"
      >
        <Filter size={12} aria-hidden className="shrink-0 text-purple-500" />
        <span className="min-w-0">
          <b className="font-semibold text-purple-600">Partial migration</b> · {names}
          {more > 0 && ` and ${more} more`}
        </span>
        <span className="ml-auto text-xs text-gray-500">
          {scope.projects.length} of {scope.total} projects
        </span>
        {onChange && (
          <Button variant="ghost" onClick={onChange}>
            Change
          </Button>
        )}
      </div>
    );
  }

  if (scope.previouslyMigrated === 0) return null;

  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-3 rounded-md border border-gray-75 bg-gray-50 px-3.5 py-2.5 text-[13px] text-gray-800"
    >
      <Layers size={12} aria-hidden className="shrink-0 text-gray-500" />
      <span>
        <b className="font-semibold text-gray-900">Full migration</b> · completing the partial run
        {scope.previouslyMigratedAt ? ` from ${formatDate(scope.previouslyMigratedAt)}` : ''}
      </span>
      {onChange && (
        <Button variant="ghost" onClick={onChange} className="ml-auto">
          Change
        </Button>
      )}
    </div>
  );
}

/** Loads the current scope for pages that do not already have it. */
export function useScopeBand(): ScopeBandData | null {
  const [scope, setScope] = useState<ScopeBandData | null>(null);

  useEffect(() => {
    let live = true;
    getScope()
      .then((resp) => {
        if (!live) return;
        setScope({
          mode: resp.scope.mode,
          projects: resp.run.resolution.projects,
          total: resp.projects.length,
          previouslyMigrated: resp.run.previously_migrated?.length ?? 0,
          previouslyMigratedAt: resp.run.previously_migrated_at,
          topUp: resp.top_up ?? null,
        });
      })
      // Without a scope the band has nothing to say; the page is still correct.
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);

  return scope;
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}
