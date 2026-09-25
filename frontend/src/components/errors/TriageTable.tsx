import { ChevronDown, ChevronRight, Gauge, GitBranch, KeyRound, RotateCw } from 'lucide-react';
import { ReactNode } from 'react';
import { Button, Checkbox, Pill, StatusIcon } from '@/components/ui/primitives';
import type { ErrorClassSummary, ErrorRecord, RemedyKind } from '@/types/errors';

/**
 * Triage: one row per cause, sorted by how much each broke.
 *
 * This is the surface for scale. Two hundred and sixty-nine failures are four
 * rows here, and the remedy column makes it obvious that these are different
 * kinds of problem — one wants a credential, one wants a setting, one wants a
 * decision, one wants something done outside the tool.
 *
 * It is also the only place with per-instance selection. Everywhere else a class
 * is treated whole, because everywhere else the useful question is "what broke",
 * not "which of these two hundred do I want back today".
 */

const REMEDY_ICONS: Partial<Record<RemedyKind, typeof KeyRound>> = {
  token: KeyRound,
  pace: Gauge,
  decision: GitBranch,
  recheck: RotateCw,
};

const COLUMNS = 'grid grid-cols-[1fr_96px_70px_180px] items-center gap-3';

export interface TriageTableProps {
  classes: ErrorClassSummary[];
  expandedClass: string | null;
  onToggleClass: (key: string) => void;
  /** Instances of the expanded class, already filtered. */
  records: ErrorRecord[];
  recordsTruncated: boolean;
  selectedIds: Set<string>;
  onToggleRecord: (id: string) => void;
  onSelectAll: () => void;
  onSelectNone: () => void;
  projectFilter: string[];
  onToggleProject: (project: string) => void;
  onApply: (cls: ErrorClassSummary) => void;
  busyClass?: string | null;
  children?: ReactNode;
}

export default function TriageTable({
  classes,
  expandedClass,
  onToggleClass,
  records,
  recordsTruncated,
  selectedIds,
  onToggleRecord,
  onSelectAll,
  onSelectNone,
  projectFilter,
  onToggleProject,
  onApply,
  busyClass,
}: TriageTableProps) {
  return (
    <div className="overflow-hidden rounded-md border border-gray-75 bg-gray-0">
      <div className={`${COLUMNS} border-b border-gray-75 bg-gray-50 px-3.5 py-2.5`}>
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">Cause</span>
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">Phase</span>
        <span className="text-right text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">
          Count
        </span>
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">
          Remedy
        </span>
      </div>

      {classes.map((cls) => {
        const expanded = expandedClass === cls.key;
        const resolved = cls.open_count === 0;
        const Icon = REMEDY_ICONS[cls.remedy.kind];

        return (
          <div key={cls.key}>
            <div
              className={`${COLUMNS} border-b border-gray-75 px-3.5 py-3 text-[13px] ${
                resolved ? 'bg-green-50' : cls.severity === 'warning' ? 'bg-yellow-50' : 'bg-red-50'
              }`}
            >
              <button
                type="button"
                onClick={() => onToggleClass(cls.key)}
                aria-expanded={expanded}
                className="flex min-w-0 items-start gap-2.5 text-left"
              >
                {resolved ? (
                  <StatusIcon status="completed" size={13} className="mt-0.5" />
                ) : expanded ? (
                  <ChevronDown size={13} className="mt-1 shrink-0 text-gray-400" />
                ) : (
                  <ChevronRight size={13} className="mt-1 shrink-0 text-gray-400" />
                )}
                <span className="min-w-0">
                  <span className="block font-semibold text-gray-900">{cls.title}</span>
                  <span className="block text-xs text-gray-500">
                    {resolved ? cls.resolution : cls.cause.split('. ')[0] + '.'}
                  </span>
                </span>
              </button>

              <span className="text-[12.5px] text-gray-500">{cls.resource_label}</span>

              <span
                className={`text-right font-mono tabular-nums ${
                  resolved ? 'text-[13px] text-green-500' : 'text-[15px] text-red-600'
                }`}
              >
                {resolved ? cls.count : cls.open_count}
              </span>

              <div>
                {resolved ? (
                  <span className="font-mono text-xs text-gray-500">
                    {cls.resolved_at ? formatTime(cls.resolved_at) : 'resolved'}
                  </span>
                ) : cls.remedy.kind === 'none' ? (
                  <Pill tone="waiting">No action needed</Pill>
                ) : (
                  <Button
                    variant={cls.remedy.kind === 'token' ? 'primary' : 'secondary'}
                    onClick={() => onApply(cls)}
                    disabled={busyClass === cls.key}
                  >
                    {Icon && <Icon size={13} />}
                    {busyClass === cls.key ? 'Working…' : cls.remedy.label}
                  </Button>
                )}
              </div>
            </div>

            {expanded && !resolved && (
              <div className="border-b border-gray-75 bg-gray-25 px-3.5 pb-3 pl-9">
                <SelectionBar
                  cls={cls}
                  records={records}
                  selectedIds={selectedIds}
                  onSelectAll={onSelectAll}
                  onSelectNone={onSelectNone}
                  onApply={() => onApply(cls)}
                  busy={busyClass === cls.key}
                />

                {(cls.projects?.length ?? 0) > 1 && (
                  <div className="mb-2 flex flex-wrap gap-2">
                    {cls.projects!.map((project) => (
                      <button
                        key={project}
                        type="button"
                        onClick={() => onToggleProject(project)}
                        className={`inline-flex h-[22px] items-center gap-1.5 rounded-md px-2 text-xs font-medium ${
                          projectFilter.includes(project)
                            ? 'bg-purple-100 text-purple-500'
                            : 'bg-gray-50 text-gray-500 hover:bg-gray-75'
                        }`}
                      >
                        {project}
                      </button>
                    ))}
                  </div>
                )}

                {records.map((rec) => (
                  <label
                    key={rec.id}
                    className="flex cursor-pointer items-center gap-2.5 border-b border-gray-75 py-1.5 text-[12.5px] last:border-b-0"
                  >
                    <Checkbox
                      checked={selectedIds.has(rec.id)}
                      onChange={() => onToggleRecord(rec.id)}
                    />
                    <span className="min-w-0 flex-1 truncate font-mono text-gray-700">{rec.item}</span>
                    {rec.size && <span className="shrink-0 font-mono text-[11.5px] text-gray-400">{rec.size}</span>}
                  </label>
                ))}

                {recordsTruncated && (
                  <p className="pt-2 text-[12px] text-gray-500">
                    Showing the first {records.length}. Filter by project to narrow this down.
                  </p>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function SelectionBar({
  cls,
  records,
  selectedIds,
  onSelectAll,
  onSelectNone,
  onApply,
  busy,
}: {
  cls: ErrorClassSummary;
  records: ErrorRecord[];
  selectedIds: Set<string>;
  onSelectAll: () => void;
  onSelectNone: () => void;
  onApply: () => void;
  busy: boolean;
}) {
  const selected = records.filter((r) => selectedIds.has(r.id));
  const weight = selected.reduce((sum, r) => sum + (r.weight ?? 0), 0);

  return (
    <div className="my-3 flex flex-wrap items-center gap-3 rounded-md border border-purple-200 bg-purple-50 px-3 py-2.5 text-[12.5px] text-purple-600">
      {/* Summarises the selection below rather than controlling it. */}
      <Checkbox checked={selected.length > 0} readOnly tabIndex={-1} aria-hidden />
      <span>
        <span className="font-semibold">
          {selected.length} of {cls.open_count} selected
        </span>
        {weight > 0 && <> — {weight.toLocaleString()} secrets will be rewritten</>}
      </span>
      <span className="ml-auto flex gap-2">
        <Button variant="secondary" onClick={selected.length ? onSelectNone : onSelectAll}>
          {selected.length ? 'Select None' : `Select All ${cls.open_count}`}
        </Button>
        <Button onClick={onApply} disabled={busy || selected.length === 0}>
          {busy ? 'Working…' : 'Fix And Re-run'}
        </Button>
      </span>
    </div>
  );
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}
