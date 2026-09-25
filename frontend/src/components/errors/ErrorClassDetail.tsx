import { Link } from 'react-router-dom';
import { Block, BlockRow, Callout, Eyebrow, Pill, StatusIcon } from '@/components/ui/primitives';
import EvidencePanel from './EvidencePanel';
import FixBox from './FixBox';
import type { ErrorClassSummary, ErrorRecord, ResolveRequest } from '@/types/errors';

/**
 * One error class in full: what it is, why it happened, the exchange that proves
 * it, what it hit, and the single action that clears all of it.
 *
 * The affected list is a sample with a link onward, not the whole thing. A class
 * can hold hundreds of members, and a pane that tries to list them all stops
 * being a diagnosis and becomes a scroll.
 */

const AFFECTED_SAMPLE = 3;

export default function ErrorClassDetail({
  cls,
  records,
  totalRecords,
  busy,
  onApplyFix,
  onDismiss,
}: {
  cls: ErrorClassSummary;
  records: ErrorRecord[];
  totalRecords: number;
  busy?: boolean;
  onApplyFix: (req: ResolveRequest) => void;
  onDismiss?: () => void;
}) {
  const resolved = cls.open_count === 0;
  const sample = records.slice(0, AFFECTED_SAMPLE);
  const projectCount = cls.projects?.length ?? 0;

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start justify-between gap-3.5">
        <div className="min-w-0">
          <h2 className="text-[18px] font-semibold tracking-[-0.01em] text-gray-900">{cls.title}</h2>
          <p className="mt-0.5 text-[12.5px] text-gray-500">
            {describeScope(cls)} · {cls.phase_name.replace(/_/g, ' ')}
            {cls.first_seen && <> · first seen {formatTime(cls.first_seen)}</>}
          </p>
        </div>
        {resolved ? (
          <Pill tone="done">Resolved</Pill>
        ) : (
          <Pill tone={cls.severity === 'warning' ? 'warning' : 'error'}>
            {cls.sample.status_text || 'Failed'}
          </Pill>
        )}
      </div>

      <Callout tone={cls.severity === 'warning' ? 'warning' : 'error'}>{cls.cause}</Callout>

      {resolved && cls.resolution && (
        <Callout tone="success">
          <span className="font-semibold text-gray-900">Resolved.</span> {cls.resolution}
          {cls.resolved_at && <> at {formatTime(cls.resolved_at)}.</>}
        </Callout>
      )}

      <EvidencePanel evidence={cls.sample} />

      {sample.length > 0 && (
        <div>
          <Eyebrow className="mb-1.5">
            Affected · {cls.count} {cls.resource_label.toLowerCase()}
            {projectCount > 0 && <> across {projectCount} {projectCount === 1 ? 'project' : 'projects'}</>}
          </Eyebrow>
          <Block>
            {sample.map((rec) => (
              <BlockRow key={rec.id}>
                <StatusIcon status={rec.resolved ? 'completed' : 'failed'} />
                <span className="min-w-0 flex-1 truncate font-mono">{rec.item}</span>
                {rec.size && <span className="shrink-0 text-gray-500">{rec.size}</span>}
              </BlockRow>
            ))}
            {totalRecords > sample.length && (
              <BlockRow>
                <span className="flex-1 text-gray-500">and {totalRecords - sample.length} more</span>
                <Link
                  to={`/migrate/errors?class=${encodeURIComponent(cls.key)}`}
                  className="text-[12.5px] font-medium text-purple-500 hover:underline"
                >
                  Open in triage
                </Link>
              </BlockRow>
            )}
          </Block>
        </div>
      )}

      {!resolved && (
        <FixBox
          remedy={cls.remedy}
          scopeSentence={fixScopeSentence(cls)}
          busy={busy}
          onApply={onApplyFix}
          onDismiss={onDismiss}
        />
      )}
    </div>
  );
}

function describeScope(cls: ErrorClassSummary): string {
  const noun = cls.resource_label.toLowerCase();
  if (cls.open_count === 0) return `${cls.count} ${noun}, all resolved`;
  if (cls.resolved_count > 0) return `${cls.open_count} of ${cls.count} ${noun} still open`;
  return `${cls.count} ${noun}`;
}

/**
 * The sentence that states what a fix will touch. It lives next to the button
 * rather than inside its label, so the number stays right while the run keeps
 * recording failures.
 */
function fixScopeSentence(cls: ErrorClassSummary): string {
  const noun = cls.resource_label.toLowerCase();
  const parts = [`${cls.open_count} ${noun}`];
  if (cls.weight > 0) parts.push(`${cls.weight.toLocaleString()} secrets`);
  return `This covers ${parts.join(' · ')}.`;
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}
