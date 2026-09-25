import { Check, AlertCircle, Minus } from 'lucide-react';
import type { ErrorClassSummary } from '@/types/errors';

/**
 * The phase timeline, with error classes nested underneath the phase that
 * produced them.
 *
 * The nesting is the point. Errors are not siblings of phases — an error belongs
 * to the phase that raised it, and showing the two at the same level makes a
 * six-phase run read as a list of fourteen unrelated things. Classes render
 * smaller, indented behind a hairline, and carry a count rather than expanding
 * into their instances: a class with three hundred members has to occupy one
 * line, or the sidebar stops being readable at exactly the moment it matters.
 */

/**
 * `skipped` is a phase that had nothing to do, and finished without running:
 * no roles to create, or workplace settings a partial run leaves for the full
 * one. It is set apart from `pending` because "did not run, on purpose" and
 * "has not run yet" call for different reactions.
 */
export type PhaseState = 'done' | 'running' | 'pending' | 'failed' | 'paused' | 'skipped';

export interface TimelinePhase {
  key: string;
  name: string;
  state: PhaseState;
  /** Right-aligned: a timestamp, a duration, or "running". */
  meta?: string;
  /** One line of result under the name. */
  summary?: string;
  classes: ErrorClassSummary[];
}

/** How many classes show before the rest collapse into a count. */
const MAX_VISIBLE_CLASSES = 3;

function dotClasses(state: PhaseState): string {
  switch (state) {
    case 'done':
      return 'bg-green-500 border-green-500';
    case 'running':
      return 'bg-purple-500 border-purple-500 ring-4 ring-purple-100';
    case 'failed':
      return 'bg-yellow-500 border-yellow-500';
    case 'paused':
      return 'bg-gray-400 border-gray-400';
    case 'skipped':
      return 'bg-gray-200 border-gray-200';
    default:
      return 'bg-gray-0 border-gray-100';
  }
}

export default function PhaseTimeline({
  phases,
  selectedClass,
  onSelectClass,
}: {
  phases: TimelinePhase[];
  selectedClass?: string | null;
  onSelectClass?: (classKey: string) => void;
}) {
  return (
    <nav aria-label="Migration phases" className="relative pl-[26px]">
      <span
        aria-hidden
        className="absolute left-[5px] top-1.5 bottom-1.5 w-0.5 rounded bg-gray-75"
      />
      {phases.map((phase) => (
        <div key={phase.key} className={`relative pb-4 last:pb-0 ${phase.state === 'pending' ? 'opacity-50' : ''}`}>
          <span
            aria-hidden
            className={`absolute -left-[26px] top-0.5 grid h-[13px] w-[13px] place-items-center rounded-full border-2 text-[7px] text-white ${dotClasses(phase.state)}`}
          >
            {phase.state === 'done' && <Check size={7} strokeWidth={4} />}
            {phase.state === 'failed' && <AlertCircle size={7} strokeWidth={4} />}
            {phase.state === 'skipped' && <Minus size={7} strokeWidth={4} />}
          </span>

          <div className="flex items-baseline gap-2">
            <span
              className={`text-[13px] font-semibold ${phase.state === 'skipped' ? 'text-gray-500' : 'text-gray-900'}`}
            >
              {phase.name}
            </span>
            {phase.state === 'skipped' && (
              <span className="shrink-0 rounded-sm bg-gray-50 px-1.5 py-px text-[10.5px] font-medium text-gray-500">
                Skipped
              </span>
            )}
            {phase.meta && (
              <span className="ml-auto shrink-0 font-mono text-[10.5px] text-gray-400">{phase.meta}</span>
            )}
          </div>
          {phase.summary && <p className="text-xs text-gray-500">{phase.summary}</p>}

          {phase.classes.length > 0 && (
            <ul className="mt-1.5 flex flex-col gap-px border-l border-gray-75 pl-2.5">
              {phase.classes.slice(0, MAX_VISIBLE_CLASSES).map((cls) => (
                <li key={cls.key}>
                  <ErrorClassRow
                    cls={cls}
                    selected={selectedClass === cls.key}
                    onSelect={onSelectClass}
                  />
                </li>
              ))}
              {phase.classes.length > MAX_VISIBLE_CLASSES && (
                <li className="px-[7px] py-1 text-[11.5px] italic text-gray-400">
                  {phase.classes.length - MAX_VISIBLE_CLASSES} more{' '}
                  {phase.classes.length - MAX_VISIBLE_CLASSES === 1 ? 'class' : 'classes'}
                </li>
              )}
            </ul>
          )}
        </div>
      ))}
    </nav>
  );
}

function ErrorClassRow({
  cls,
  selected,
  onSelect,
}: {
  cls: ErrorClassSummary;
  selected: boolean;
  onSelect?: (key: string) => void;
}) {
  const resolved = cls.open_count === 0;

  // A resolved class stays on the timeline rather than disappearing. The run's
  // history should not rewrite itself as things get fixed.
  const swatch = resolved
    ? 'bg-green-500'
    : cls.severity === 'warning'
      ? 'bg-yellow-500'
      : 'bg-red-500';

  return (
    <button
      type="button"
      onClick={() => onSelect?.(cls.key)}
      aria-current={selected ? 'true' : undefined}
      className={`flex w-full items-center gap-2 rounded-sm border-l-2 px-[7px] py-1 text-left text-[11.5px] leading-tight transition-colors ${
        selected
          ? 'border-red-500 bg-red-50 font-semibold text-red-600'
          : 'border-transparent text-gray-600 hover:bg-gray-50'
      } ${resolved && !selected ? 'text-gray-400' : ''}`}
    >
      <span aria-hidden className={`h-[5px] w-[5px] shrink-0 rounded-[1px] ${swatch}`} />
      <span className="min-w-0 flex-1 truncate">{cls.title}</span>
      <span className={`shrink-0 font-mono text-[10.5px] ${selected ? 'text-red-500' : 'text-gray-400'}`}>
        {resolved ? 'fixed' : cls.open_count}
      </span>
    </button>
  );
}
