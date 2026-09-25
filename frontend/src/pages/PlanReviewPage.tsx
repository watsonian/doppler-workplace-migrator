import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowRight, Download, Loader2 } from 'lucide-react';
import {
  Block,
  BlockHeader,
  Button,
  Callout,
  FooterBar,
  PageHeading,
  Pill,
  Radio,
} from '@/components/ui/primitives';
import DiffRollup from '@/components/diff/DiffRollup';
import { getCompare, getPlan, recordNoChangeCheck, setRunOptions } from '@/api/client';
import { ScopeBand, useScopeBand } from '@/components/scope/ScopeBand';
import { SCIM_DOCS_URL } from '@/lib/links';
import type { CompareResponse, Difference, PlanResponse } from '@/api/client';
import type { DriftPolicy } from '@/types/diff';

/**
 * Review — what this run will change, not an inventory of what exists.
 *
 * The last two blocks are separate because the decisions in them are opposite in
 * shape. Deferred is opt-in: nothing is deleted unless you tick it. Changed On
 * The Target is opt-out: those will be overwritten unless you say otherwise.
 * Getting those the wrong way round is how a tool deletes something nobody asked
 * it to, so both sit as controls in the block they belong to rather than as a
 * button in the footer.
 */
export default function PlanReviewPage() {
  const navigate = useNavigate();

  const [compare, setCompare] = useState<CompareResponse | null>(null);
  const [plan, setPlan] = useState<PlanResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [deleteSecrets, setDeleteSecrets] = useState(false);
  const [deleteConfigs, setDeleteConfigs] = useState(false);
  const [drift, setDrift] = useState<DriftPolicy>('overwrite');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const scope = useScopeBand();
  const topUp = Boolean(scope?.topUp && scope.mode === 'full');

  // A top-up that found nothing is recorded — "checked, nothing had changed"
  // tells the next person the target is current — and goes back to waiting
  // rather than through an empty run.
  async function backToWaiting(recordCheck: boolean) {
    setSaving(true);
    setSaveError(null);
    try {
      if (recordCheck) await recordNoChangeCheck();
      navigate('/standby');
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Could not record the check');
      setSaving(false);
    }
  }

  // The choices go to the server, where the run reads them. They used to ride
  // along as navigation state that no page downstream ever read, so every run
  // went ahead on defaults whatever was ticked here.
  async function proceed() {
    setSaving(true);
    setSaveError(null);
    try {
      await setRunOptions({
        delete_orphans: deleteSecrets,
        delete_orphan_configs: deleteConfigs,
        drift,
      });
      navigate('/migrate');
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Could not save these choices');
    } finally {
      setSaving(false);
    }
  }

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [cmp, planResp] = await Promise.all([
        getCompare().catch(() => null),
        getPlan().catch(() => null),
      ]);
      if (cmp && 'differences' in cmp) setCompare(cmp as CompareResponse);
      if (planResp) setPlan(planResp);
      if (!cmp && !planResp) setError('Neither the comparison nor the plan could be loaded');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load the plan');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) {
    return (
      <div className="flex items-center justify-center gap-2.5 py-20 text-[13px] text-gray-500">
        <Loader2 size={16} className="animate-spin-slow text-purple-500" />
        Loading the plan…
      </div>
    );
  }

  if (error) {
    return (
      <div className="mx-auto w-full max-w-[720px] px-6 py-8">
        <Callout tone="error">{error}</Callout>
        <div className="mt-4">
          <Button variant="secondary" onClick={load}>
            Try Again
          </Button>
        </div>
      </div>
    );
  }

  const differences = compare?.differences ?? [];
  const counts = compare?.counts;
  const driftRecords = compare?.drift ?? [];

  const creates = byKind(differences, 'create');
  const updates = byKind(differences, 'update');
  const orphans = byKind(differences, 'orphan');

  const orphanSecrets = orphans.secret ?? [];
  const orphanConfigs = orphans.config ?? [];
  const deferredTotal = orphanSecrets.length + orphanConfigs.length;
  const nothingToWrite = Boolean(counts && counts.create === 0 && counts.update === 0 && deferredTotal === 0);

  return (
    <div className="mx-auto w-full max-w-[860px] px-6 py-6">
      <PageHeading
        title="What This Run Will Change"
        subtitle={
          counts
            ? `${counts.create.toLocaleString()} additions, ${counts.update.toLocaleString()} updates, ${counts.match.toLocaleString()} already current${
                counts.drift > 0 ? `, ${counts.drift} needing a decision` : ''
              }${deferredTotal > 0 ? `, ${deferredTotal} deletions deferred` : ''}.`
            : 'Nothing has been written to the target.'
        }
      />

      <div className="mt-5 flex flex-col gap-3.5">
        <ScopeBand
          scope={scope}
          onChange={() => navigate('/scope')}
          onBackToWaiting={() => void backToWaiting(false)}
        />
        {saveError && <Callout tone="error">{saveError}</Callout>}

        {plan?.user_check?.missing && plan.user_check.missing.length > 0 && (
          <Callout tone="warning">
            <span className="font-semibold text-gray-900">
              {plan.user_check.missing.length} users are missing on the target.
            </span>{' '}
            The API cannot create users, so every access control naming one is skipped. Provision
            them with{' '}
            <a
              href={SCIM_DOCS_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="font-medium text-purple-500 hover:underline"
            >
              SCIM
            </a>
            , then compare again.
          </Callout>
        )}

        {counts && counts.create > 0 && (
          <Block>
            <BlockHeader title={`Additions · ${counts.create.toLocaleString()}`}>
              <Pill tone="running">Will be created</Pill>
            </BlockHeader>
            {rollupsFor(creates, 'create')}
          </Block>
        )}

        {counts && counts.update > 0 && (
          <Block>
            <BlockHeader title={`Updates · ${counts.update.toLocaleString()}`}>
              <Pill tone="warning">Source has changed</Pill>
            </BlockHeader>
            {rollupsFor(updates, 'update')}
          </Block>
        )}

        {deferredTotal > 0 && (
          <Block>
            <BlockHeader title={`Deferred To Cutover · ${deferredTotal}`}>
              <Pill tone="skipped">Tick to do now</Pill>
            </BlockHeader>

            {orphanSecrets.length > 0 && (
              <DiffRollup
                label="Delete secrets removed from the source"
                detail="Migrated by an earlier run, then deleted from the source."
                count={orphanSecrets.length}
                checked={deleteSecrets}
                onToggle={() => setDeleteSecrets((v) => !v)}
                defaultOpen
                columns={[
                  { label: 'Config', width: '170px', mono: true, muted: true },
                  { label: 'Secret', width: '1fr', mono: true },
                ]}
                rows={orphanSecrets.map((d) => [d.scope ?? '', d.name])}
              />
            )}

            {orphanConfigs.length > 0 && (
              <DiffRollup
                label="Delete configs removed from the source"
                detail="Deleting a config takes its secrets with it, so this stays separate."
                count={orphanConfigs.length}
                checked={deleteConfigs}
                onToggle={() => setDeleteConfigs((v) => !v)}
                columns={[{ label: 'Config', width: '1fr', mono: true }]}
                rows={orphanConfigs.map((d) => [d.scope ?? d.name])}
              />
            )}

            <div className="bg-gray-25 px-3.5 py-2 text-[12.5px] text-gray-500">
              Left unticked, these happen at cutover instead. Either way they show up in the plan of
              the run that does them.
            </div>
          </Block>
        )}

        {driftRecords.length > 0 && (
          <Block>
            <BlockHeader title={`Changed On The Target · ${driftRecords.length}`}>
              <Pill tone="error">Choose one</Pill>
            </BlockHeader>

            <DiffRollup
              label="Edited on the target since this migrator wrote them"
              count={driftRecords.length}
              defaultOpen
              columns={[
                { label: 'Config', width: '170px', mono: true, muted: true },
                { label: 'Changed', width: '150px', muted: true, align: 'right' },
                { label: 'Changed by', width: '1fr' },
              ]}
              rows={driftRecords.map((d) => [d.scope, formatTime(d.changed_at), d.changed_by])}
            />

            <div className="flex flex-col gap-2 border-t border-gray-75 px-3.5 py-3">
              <label className="flex cursor-pointer items-start gap-2.5 text-[13px]">
                <Radio
                  name="drift"
                  className="mt-0.5"
                  checked={drift === 'overwrite'}
                  onChange={() => setDrift('overwrite')}
                />
                <span>
                  <b className="text-gray-900">Overwrite with the source</b>
                  <br />
                  <span className="text-xs text-gray-500">
                    Treats the source as correct. The target&rsquo;s current value is not
                    recoverable from here afterwards.
                  </span>
                </span>
              </label>
              <label className="flex cursor-pointer items-start gap-2.5 text-[13px]">
                <Radio
                  name="drift"
                  className="mt-0.5"
                  checked={drift === 'keep_target'}
                  onChange={() => setDrift('keep_target')}
                />
                <span>
                  <b className="text-gray-900">Keep the target&rsquo;s value</b>
                  <br />
                  <span className="text-xs text-gray-500">
                    Skips those configs and records them in the summary. The next run will raise
                    them again.
                  </span>
                </span>
              </label>
            </div>
          </Block>
        )}

        {driftRecords.length > 0 && (
          <Callout tone="warning">
            Something changed those on the target after this migrator wrote them. Whether it was a
            person or another tool is worth knowing before deciding the source is the one to trust —
            a provisioning tool writing to a config means something else owns it, and overwriting
            starts a fight the migrator loses on the next apply.
          </Callout>
        )}

        {compare?.drift_unavailable && (
          <Callout tone="warning">
            <span className="font-semibold text-gray-900">
              Changes made on the target could not be checked.
            </span>{' '}
            {compare.drift_unavailable}
          </Callout>
        )}

        {nothingToWrite && (
          <Callout tone="success">
            The target already matches the source.{' '}
            {topUp
              ? 'Nothing has changed since the last top-up.'
              : `${counts!.match.toLocaleString()} items are current and this run would write nothing.`}
          </Callout>
        )}

        <FooterBar
          left={
            <Button variant="ghost" onClick={() => navigate('/compare')}>
              Back
            </Button>
          }
          right={
            <>
              <Button variant="ghost" onClick={() => exportDiff(compare, plan)}>
                <Download size={13} /> Export Diff
              </Button>
              {topUp && nothingToWrite ? (
                <Button size="md" onClick={() => void backToWaiting(true)} disabled={saving}>
                  Back To Waiting <ArrowRight size={14} />
                </Button>
              ) : (
                <Button size="md" onClick={proceed} disabled={saving}>
                  {saving ? 'Saving…' : topUp ? 'Top Up' : 'Continue'} <ArrowRight size={14} />
                </Button>
              )}
            </>
          }
        />
      </div>
    </div>
  );
}

type ByKind = Partial<Record<string, Difference[]>>;

function byKind(differences: Difference[], disposition: string): ByKind {
  const out: ByKind = {};
  for (const diff of differences) {
    if (diff.disposition !== disposition) continue;
    (out[diff.kind] ??= []).push(diff);
  }
  return out;
}

/** How each resource kind reads as a rollup label. */
const KIND_LABELS: Record<string, string> = {
  workplace_role: 'Workplace roles',
  project_role: 'Project roles',
  group: 'Groups',
  group_members: 'Group membership',
  service_account: 'Service accounts',
  project: 'Projects',
  environment: 'Environments',
  config: 'Configs',
  secret: 'Secrets',
  webhook: 'Webhooks',
  project_member: 'Project members',
  trusted_ips: 'Trusted IP ranges',
};

/**
 * Where an item lives, for the kinds where that says something.
 *
 * Workplace resources — roles, groups, service accounts — live nowhere in
 * particular, and a project is its own location, so those get no column at
 * all rather than a blank one or one repeating the name. A config is placed by
 * its project, not by "project/config", which would repeat it too.
 */
const LOCATIONS: Partial<Record<string, { label: string; of: (d: Difference) => string }>> = {
  environment: { label: 'Project', of: (d) => d.project ?? '' },
  config: { label: 'Project', of: (d) => d.project ?? '' },
  webhook: { label: 'Project', of: (d) => d.project ?? '' },
  project_member: { label: 'Project', of: (d) => d.project ?? '' },
  secret: { label: 'Config', of: (d) => d.scope ?? '' },
};

/** What identifies the item itself. A trusted-IP list is named for its config. */
function itemOf(d: Difference): string {
  if (d.kind === 'trusted_ips') return d.scope ?? d.name;
  // A member's name is "group:<uuid>"; its label says which group.
  return d.label ?? d.name;
}

/** The order a new project's contents read in, with singular and plural. */
const CONTENT_NOUNS: [string, string, string][] = [
  ['environment', 'environment', 'environments'],
  ['config', 'config', 'configs'],
  ['secret', 'secret', 'secrets'],
  ['webhook', 'webhook', 'webhooks'],
  ['project_member', 'member', 'members'],
];

/**
 * What a new project brings with it: "3 environments, 3 configs, 145 secrets".
 * Those are created with the project rather than listed one by one.
 */
export function describeContents(contains: Difference['contains']): string {
  const parts = CONTENT_NOUNS.flatMap(([kind, one, many]) => {
    const n = contains?.[kind as keyof NonNullable<Difference['contains']>] ?? 0;
    return n > 0 ? [`${n.toLocaleString()} ${n === 1 ? one : many}`] : [];
  });
  return parts.length > 0 ? parts.join(', ') : 'Empty';
}

/**
 * One rollup per resource kind.
 *
 * An update carries its field changes, so it gets a was/will-be shape; a create
 * has nothing to compare against and is simply a list. The item always has its
 * own column — an update used to show only where it lived, so two changed
 * secrets in one config read as the same row twice.
 */
function rollupsFor(groups: ByKind, disposition: 'create' | 'update') {
  return Object.entries(groups).map(([kind, items]) => {
    if (!items || items.length === 0) return null;

    const label = KIND_LABELS[kind] ?? kind;
    const location = LOCATIONS[kind];
    const where = location
      ? [{ label: location.label, width: '170px', mono: true, muted: true }]
      : [];

    const columns =
      disposition === 'update'
        ? [
            ...where,
            { label, width: '1fr', mono: true },
            { label: 'Field', width: '110px' },
            { label: 'Was', width: '1fr', mono: true },
            { label: 'Will be', width: '1fr', mono: true },
          ]
        : kind === 'project'
          ? [
              { label, width: '1fr', mono: true },
              { label: 'Contents', width: '2fr' },
            ]
          : [...where, { label, width: '1fr', mono: true }];

    const place = (d: Difference) => (location ? [location.of(d)] : []);

    const rows =
      disposition === 'update'
        ? items.flatMap((d) =>
            (d.changes ?? [{ field: 'changed', was: '', now: '' }]).map((c) => {
              // A secret's value is never sent, so its row says so rather than
              // showing a dash that reads as "empty".
              const hidden = d.kind === 'secret' && c.field === 'value';
              return [
                ...place(d),
                itemOf(d),
                c.field || 'changed',
                hidden ? 'not shown' : c.was || '—',
                hidden ? 'not shown' : c.now || '—',
              ];
            }),
          )
        : kind === 'project'
          ? items.map((d) => [itemOf(d), describeContents(d.contains)])
          : items.map((d) => [...place(d), itemOf(d)]);

    return (
      <DiffRollup
        key={kind}
        label={label}
        count={items.length}
        columns={columns}
        rows={rows}
        defaultOpen={items.length <= 4}
      />
    );
  });
}

/**
 * The export carries the diff, which says which secrets differ and never what
 * they differ to — the same rule the screen follows, for the same reason.
 */
function exportDiff(compare: CompareResponse | null, plan: PlanResponse | null) {
  const blob = new Blob([JSON.stringify({ compare, plan }, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'migration-diff.json';
  a.click();
  URL.revokeObjectURL(url);
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}
