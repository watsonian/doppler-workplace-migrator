import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { AlertTriangle, ArrowLeft, CheckCircle2, Download } from 'lucide-react';
import { Button, Callout, FooterBar, PageHeading } from '@/components/ui/primitives';
import TriageTable from '@/components/errors/TriageTable';
import FixBox from '@/components/errors/FixBox';
import EvidencePanel from '@/components/errors/EvidencePanel';
import {
  errorsExportURL,
  getErrorClass,
  getErrors,
  resolveErrorClass,
} from '@/api/client';
import type {
  ErrorClassSummary,
  ErrorRecord,
  ErrorsResponse,
  ResolveRequest,
} from '@/types/errors';
import { useSSEEvent } from '@/hooks/useSSE';

/**
 * Triage — the surface for scale.
 *
 * Reached from the migration screen when there are enough failures that a detail
 * pane stops being the right shape. Everything here works on classes, except the
 * instance list inside an expanded class, which is the one place per-item
 * selection makes sense.
 */
export default function ErrorTriagePage() {
  const [params, setParams] = useSearchParams();
  const [data, setData] = useState<ErrorsResponse | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const expandedClass = params.get('class');
  const [records, setRecords] = useState<ErrorRecord[]>([]);
  const [recordsTruncated, setRecordsTruncated] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [projectFilter, setProjectFilter] = useState<string[]>([]);
  const [pendingFix, setPendingFix] = useState<ErrorClassSummary | null>(null);
  const [busyClass, setBusyClass] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setData(await getErrors());
      setLoadError(null);
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Could not load the errors');
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // The run keeps going while you triage, so the list has to keep up with it.
  useSSEEvent('errors_changed', refresh);
  useSSEEvent('rerun_complete', refresh);

  const loadRecords = useCallback(
    async (classKey: string, projects: string[]) => {
      const resp = await getErrorClass(classKey, { projects, openOnly: true });
      setRecords(resp.records);
      setRecordsTruncated(resp.truncated);
      // Everything open is selected by default: fixing the whole class is the
      // common case, and unticking a few is easier than ticking two hundred.
      setSelectedIds(new Set(resp.records.map((r) => r.id)));
    },
    [],
  );

  useEffect(() => {
    if (!expandedClass) {
      setRecords([]);
      setSelectedIds(new Set());
      return;
    }
    void loadRecords(expandedClass, projectFilter);
  }, [expandedClass, projectFilter, loadRecords]);

  const toggleClass = (key: string) => {
    setProjectFilter([]);
    setParams(key === expandedClass ? {} : { class: key });
  };

  const toggleProject = (project: string) => {
    setProjectFilter((prev) =>
      prev.includes(project) ? prev.filter((p) => p !== project) : [...prev, project],
    );
  };

  const toggleRecord = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  async function applyFix(cls: ErrorClassSummary, req: ResolveRequest) {
    setBusyClass(cls.key);
    setNotice(null);
    try {
      // When rows have been unticked, send the explicit selection. When nothing
      // has been narrowed, send no ids at all so the fix covers the whole class
      // — including failures recorded since this page loaded.
      const ids =
        expandedClass === cls.key && selectedIds.size > 0 && selectedIds.size < records.length
          ? [...selectedIds]
          : undefined;

      const resp = await resolveErrorClass(cls.key, {
        ...req,
        ids,
        projects: ids ? undefined : projectFilter.length ? projectFilter : undefined,
      });

      setNotice(
        resp.status === 'resolved'
          ? `${resp.resolution}. ${resp.resolved ?? 0} cleared.`
          : `Re-running ${resp.item_count} ${resp.item_count === 1 ? 'item' : 'items'}. ${resp.resolution}`,
      );
      setPendingFix(null);
      await refresh();
    } catch (err) {
      setNotice(err instanceof Error ? err.message : 'The fix could not be applied');
    } finally {
      setBusyClass(null);
    }
  }

  const openClasses = useMemo(() => data?.classes.filter((c) => c.open_count > 0) ?? [], [data]);
  const allClear = data !== null && data.total > 0 && data.open === 0;

  return (
    <div className="mx-auto w-full max-w-[880px] px-6 py-6">
      <div className="mb-4 flex items-center justify-between gap-3">
        <Link
          to="/migrate"
          className="inline-flex items-center gap-2 text-[13px] font-semibold text-gray-600 hover:text-gray-900"
        >
          <ArrowLeft size={14} /> Back To Run
        </Link>
        <Button
          variant="ghost"
          onClick={() => {
            window.location.href = errorsExportURL;
          }}
        >
          <Download size={14} /> Export Errors As CSV
        </Button>
      </div>

      <PageHeading
        title="Errors"
        subtitle={
          data
            ? `${data.total} recorded across ${data.class_count} ${data.class_count === 1 ? 'cause' : 'causes'}.`
            : 'Loading…'
        }
      />

      <div className="mt-5 flex flex-col gap-3.5">
        {loadError && <Callout tone="error">{loadError}</Callout>}
        {notice && <Callout tone="info">{notice}</Callout>}

        {allClear && (
          <Callout tone="success">
            <span className="font-semibold text-gray-900">No open errors.</span> All {data!.total}{' '}
            were resolved. The record below is kept for the summary and the export.
          </Callout>
        )}

        {data && data.total === 0 && (
          <Callout tone="success">
            Nothing has failed. Errors recorded during the run appear here, grouped by cause.
          </Callout>
        )}

        {data && data.open > 0 && (
          <Callout tone="error">
            <span className="font-semibold text-gray-900">{data.open} open</span> across{' '}
            {openClasses.length} {openClasses.length === 1 ? 'cause' : 'causes'}.
            {data.fixable_open > 0 && data.fixable_open !== data.open && (
              <> {data.fixable_open} of them can be cleared from here.</>
            )}{' '}
            The run continues while you triage.
          </Callout>
        )}

        {data && data.classes.length > 0 && (
          <TriageTable
            classes={data.classes}
            expandedClass={expandedClass}
            onToggleClass={toggleClass}
            records={records}
            recordsTruncated={recordsTruncated}
            selectedIds={selectedIds}
            onToggleRecord={toggleRecord}
            onSelectAll={() => setSelectedIds(new Set(records.map((r) => r.id)))}
            onSelectNone={() => setSelectedIds(new Set())}
            projectFilter={projectFilter}
            onToggleProject={toggleProject}
            onApply={setPendingFix}
            busyClass={busyClass}
          />
        )}

        {pendingFix && (
          <div className="flex flex-col gap-3">
            <div className="flex items-start gap-2.5">
              {pendingFix.severity === 'warning' ? (
                <AlertTriangle size={16} className="mt-0.5 shrink-0 text-yellow-500" />
              ) : (
                <AlertTriangle size={16} className="mt-0.5 shrink-0 text-red-500" />
              )}
              <div>
                <h2 className="text-[15px] font-semibold text-gray-900">{pendingFix.title}</h2>
                <p className="text-[12.5px] text-gray-500">{pendingFix.cause}</p>
              </div>
            </div>
            <EvidencePanel evidence={pendingFix.sample} title="What came back" />
            <FixBox
              remedy={pendingFix.remedy}
              scopeSentence={scopeSentence(pendingFix, selectedIds, records)}
              busy={busyClass === pendingFix.key}
              onApply={(req) => applyFix(pendingFix, req)}
              onDismiss={() => setPendingFix(null)}
              dismissLabel="Cancel"
            />
          </div>
        )}

        <FooterBar
          left={
            <span className="text-[12.5px] text-gray-500">
              Every fix re-runs only the affected items. Nothing already migrated is touched.
            </span>
          }
          right={
            allClear ? (
              <span className="inline-flex items-center gap-2 text-[13px] font-semibold text-green-500">
                <CheckCircle2 size={15} /> All resolved
              </span>
            ) : null
          }
        />
      </div>
    </div>
  );
}

/** States what a fix covers, from the live selection rather than a baked count. */
function scopeSentence(
  cls: ErrorClassSummary,
  selectedIds: Set<string>,
  records: ErrorRecord[],
): string {
  const narrowed = selectedIds.size > 0 && selectedIds.size < records.length;
  const count = narrowed ? selectedIds.size : cls.open_count;
  const weight = narrowed
    ? records.filter((r) => selectedIds.has(r.id)).reduce((sum, r) => sum + (r.weight ?? 0), 0)
    : cls.weight;

  const parts = [`${count} ${cls.resource_label.toLowerCase()}`];
  if (weight > 0) parts.push(`${weight.toLocaleString()} secrets`);
  return `This covers ${parts.join(' · ')}.`;
}
