import { ReactNode, useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { Checkbox, Pill, type PillTone } from '@/components/ui/primitives';

/**
 * A rollup: one row per kind of change, openable into the items behind it.
 *
 * Three items and three hundred are the same row. A screen that grows a line per
 * item stops being reviewable at exactly the scale where reviewing it matters,
 * and a count on its own tells you the shape of a change and nothing about
 * whether it is safe — nine deleted secrets is fine if they are the ones you
 * deleted, and alarming otherwise. The only way to tell is to read the names.
 */

/** A list renders up to this many rows before it cuts off and links out. */
export const MAX_VISIBLE_ITEMS = 10;

export interface Column {
  label: string;
  /** Grid track, e.g. "170px" or "1fr". */
  width: string;
  align?: 'left' | 'right';
  /** Renders as an identifier rather than prose. */
  mono?: boolean;
  /** Muted, for a secondary column. */
  muted?: boolean;
}

export interface RollupProps {
  label: string;
  count: number;
  tone?: PillTone;
  badge?: ReactNode;
  /** A short line under the label saying what qualifies an item for this group. */
  detail?: string;
  columns: Column[];
  rows: (string | number)[][];
  /** Total items, when `rows` has already been trimmed. */
  total?: number;
  onShowAll?: () => void;
  /** Renders a tick, for a rollup that is an opt-in rather than a fact. */
  checked?: boolean;
  onToggle?: () => void;
  defaultOpen?: boolean;
  /** A note under the list, for something that is not an item. */
  footnote?: ReactNode;
}

export default function DiffRollup({
  label,
  count,
  tone,
  badge,
  detail,
  columns,
  rows,
  total,
  onShowAll,
  checked,
  onToggle,
  defaultOpen = false,
  footnote,
}: RollupProps) {
  const [open, setOpen] = useState(defaultOpen);
  const selectable = checked !== undefined;

  const visible = rows.slice(0, MAX_VISIBLE_ITEMS);
  const hidden = (total ?? rows.length) - visible.length;
  const template = columns.map((c) => c.width).join(' ');

  return (
    <div>
      <div
        className={`grid items-center gap-2.5 border-b border-gray-75 px-3.5 py-2.5 text-[13px] ${
          selectable ? 'grid-cols-[26px_1fr_84px]' : 'grid-cols-[1fr_84px]'
        }`}
      >
        {selectable && (
          <Checkbox checked={Boolean(checked)} onChange={onToggle} />
        )}

        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="min-w-0 text-left"
        >
          <span className="flex items-center gap-1.5 font-semibold text-gray-900">
            {open ? (
              <ChevronDown size={11} className="shrink-0 text-gray-400" />
            ) : (
              <ChevronRight size={11} className="shrink-0 text-gray-400" />
            )}
            {label}
            {badge}
          </span>
          {detail && <span className="block text-xs text-gray-500">{detail}</span>}
        </button>

        <span className="text-right tabular-nums">
          {tone ? (
            <Pill tone={tone}>{count.toLocaleString()}</Pill>
          ) : (
            <span className="text-gray-500">{count.toLocaleString()}</span>
          )}
        </span>
      </div>

      {open && rows.length > 0 && (
        <>
          <div
            className="grid gap-3 border-b border-gray-75 bg-gray-25 px-3.5 py-2 pl-[38px] text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400"
            style={{ gridTemplateColumns: template }}
          >
            {columns.map((c) => (
              <span key={c.label} className={c.align === 'right' ? 'text-right' : ''}>
                {c.label}
              </span>
            ))}
          </div>

          {visible.map((row, i) => (
            <div
              key={i}
              className="grid gap-3 border-b border-gray-75 bg-gray-25 px-3.5 py-1.5 pl-[38px] text-[12.5px]"
              style={{ gridTemplateColumns: template }}
            >
              {row.map((cell, j) => {
                const col = columns[j];
                return (
                  <span
                    key={j}
                    className={`truncate ${col?.mono ? 'font-mono' : ''} ${
                      col?.muted ? 'text-gray-500' : 'text-gray-900'
                    } ${col?.align === 'right' ? 'text-right' : ''}`}
                  >
                    {cell}
                  </span>
                );
              })}
            </div>
          ))}

          {hidden > 0 && (
            <div className="flex items-center justify-between gap-2.5 border-b border-gray-75 bg-gray-25 px-3.5 py-2 pl-[38px] text-[12.5px] text-gray-500">
              <span>{hidden.toLocaleString()} more</span>
              {onShowAll && (
                <button type="button" onClick={onShowAll} className="font-medium text-purple-500 hover:underline">
                  Show all
                </button>
              )}
            </div>
          )}

          {footnote && (
            <div className="border-b border-gray-75 bg-gray-25 px-3.5 py-2 pl-[38px] text-[12.5px] text-gray-500">
              {footnote}
            </div>
          )}
        </>
      )}
    </div>
  );
}
