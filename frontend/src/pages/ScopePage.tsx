import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  ArrowRight,
  Bot,
  Check,
  Loader2,
  Minus,
  RotateCw,
  Search,
  ShieldCheck,
  Users,
} from 'lucide-react';
import {
  Block,
  Button,
  Callout,
  Checkbox,
  FooterBar,
  PageHeading,
  Pill,
  Radio,
  TextField,
} from '@/components/ui/primitives';
import { getScope, previewScope, setScope } from '@/api/client';
import type { IncludedResource, ScopeMode, ScopeResolution, ScopeResponse } from '@/api/client';

/**
 * Scope — migrate everything, or a few projects first.
 *
 * A partial run takes projects whole and brings only the roles, groups and
 * service accounts their members use; the workplace layer waits for the full
 * run. A full run after a partial migrates what the partial left out and brings
 * what it moved up to date, so a test never has to be undone.
 */
export default function ScopePage() {
  const navigate = useNavigate();

  const [data, setData] = useState<ScopeResponse | null>(null);
  const [mode, setMode] = useState<ScopeMode>('full');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState('');
  const [preview, setPreview] = useState<ScopeResolution | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoadError(null);
    try {
      const resp = await getScope();
      setData(resp);
      setMode(resp.scope.mode);
      setSelected(new Set(resp.scope.mode === 'partial' ? (resp.scope.projects ?? []) : []));
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : 'Could not load the gathered projects');
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Projects an earlier run moved are in every later scope, ticked and locked:
  // unticking would suggest the run removes them from the target, and nothing
  // in a migration deletes a project for being left out.
  const earlier = useMemo(
    () => new Set((data?.projects ?? []).filter((p) => p.migrated_at).map((p) => p.slug)),
    [data],
  );
  const lastPartialAt = data?.run.previously_migrated_at ?? null;

  // The dependency panel follows the selection. A preview resolves without
  // storing, so ticking does not rebuild the plan and re-check users each time.
  const seq = useRef(0);
  useEffect(() => {
    if (mode !== 'partial' || !data) {
      setPreview(null);
      return;
    }
    const projects = [...selected].filter((slug) => !earlier.has(slug));
    if (projects.length === 0 && earlier.size === 0) {
      setPreview(null);
      return;
    }
    const mine = ++seq.current;
    const timer = setTimeout(() => {
      previewScope({ mode: 'partial', projects })
        .then((r) => {
          if (mine === seq.current) setPreview(r.resolution);
        })
        .catch(() => {
          if (mine === seq.current) setPreview(null);
        });
    }, 150);
    return () => clearTimeout(timer);
  }, [mode, selected, earlier, data]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    const rows = data?.projects ?? [];
    return q ? rows.filter((p) => p.name.toLowerCase().includes(q) || p.slug.includes(q)) : rows;
  }, [data, query]);

  function toggle(slug: string) {
    if (earlier.has(slug)) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(slug)) next.delete(slug);
      else next.add(slug);
      return next;
    });
  }

  const newlyChosen = [...selected].filter((slug) => !earlier.has(slug));
  const canContinue = mode === 'full' || newlyChosen.length > 0;

  async function proceed() {
    setSaving(true);
    setSaveError(null);
    try {
      await setScope(mode === 'full' ? { mode: 'full' } : { mode: 'partial', projects: newlyChosen });
      navigate('/compare');
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Could not save the scope');
    } finally {
      setSaving(false);
    }
  }

  if (loadError) {
    return (
      <div className="mx-auto w-full max-w-[760px] px-6 py-8">
        <Callout tone="error">{loadError}</Callout>
        <div className="mt-4">
          <Button variant="secondary" onClick={load}>
            Try Again
          </Button>
        </div>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="flex items-center justify-center gap-2.5 py-20 text-[13px] text-gray-500">
        <Loader2 size={16} className="animate-spin-slow text-purple-500" />
        Loading the gathered projects…
      </div>
    );
  }

  const total = data.projects.length;
  const totals = data.projects.reduce(
    (acc, p) => ({ configs: acc.configs + p.configs, secrets: acc.secrets + p.secrets }),
    { configs: 0, secrets: 0 },
  );
  const remainingAfterEarlier = total - earlier.size;

  return (
    <div className="mx-auto w-full max-w-[940px] px-6 py-6">
      <PageHeading
        title="What To Migrate"
        subtitle={
          earlier.size > 0
            ? `A partial run${lastPartialAt ? ` on ${formatDate(lastPartialAt)}` : ''} migrated ${earlier.size} of ${total} projects.`
            : `The source has ${total} projects, ${totals.configs.toLocaleString()} configs and ${totals.secrets.toLocaleString()} secrets.`
        }
      />

      <div className="mt-5 flex flex-col gap-3.5">
        {saveError && <Callout tone="error">{saveError}</Callout>}

        <div role="radiogroup" aria-label="Migration scope" className="grid gap-3 md:grid-cols-2">
          <ModeCard
            id="scope-full"
            checked={mode === 'full'}
            onSelect={() => setMode('full')}
            title="Full Migration"
            description={earlier.size > 0 ? 'Finish the job.' : 'Everything on the source: every project, and the whole workplace layer around them.'}
            points={
              earlier.size > 0
                ? [
                    { tone: 'yes', text: `Migrate the other ${remainingAfterEarlier} ${plural(remainingAfterEarlier, 'project')}` },
                    { tone: 'yes', text: 'Migrate the rest of the workplace layer' },
                    { tone: 'update', text: `Bring the ${earlier.size} earlier ${plural(earlier.size, 'project')} up to date` },
                  ]
                : [
                    { tone: 'yes', text: `All ${total} projects` },
                    { tone: 'yes', text: 'Roles, groups, service accounts and settings' },
                    { tone: 'yes', text: 'Change-request policies' },
                  ]
            }
          />
          <ModeCard
            id="scope-partial"
            checked={mode === 'partial'}
            onSelect={() => setMode('partial')}
            title="Partial Migration"
            description={earlier.size > 0 ? 'Add more projects to the test.' : 'Selected projects only, to try the migration out before committing to it.'}
            points={
              earlier.size > 0
                ? [
                    { tone: 'yes', text: `The ${earlier.size} already migrated stay, and are brought up to date` },
                    { tone: 'yes', text: 'Pick more to add alongside them' },
                  ]
                : [
                    { tone: 'yes', text: 'The projects you pick, each whole' },
                    { tone: 'yes', text: 'Only the roles and groups they use' },
                    { tone: 'no', text: 'No workplace settings or policies' },
                  ]
            }
          />
        </div>

        {mode === 'partial' && (
          <div className="grid items-start gap-3.5 lg:grid-cols-[1fr_300px]">
            <Block>
              <div className="flex items-center gap-2.5 border-b border-gray-75 bg-gray-0 px-3 py-2.5">
                <div className="relative flex-1">
                  <Search
                    size={13}
                    aria-hidden
                    className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-300"
                  />
                  <TextField
                    id="scope-search"
                    aria-label="Search projects"
                    size="sm"
                    inset
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder={`Search ${total} projects`}
                    className="pl-8"
                  />
                </div>
                {newlyChosen.length > 0 && (
                  <Button
                    variant="ghost"
                    onClick={() => setSelected(new Set([...selected].filter((s) => earlier.has(s))))}
                  >
                    Select None
                  </Button>
                )}
              </div>

              <div className="grid grid-cols-[22px_1fr_64px_76px_128px] items-center gap-2.5 border-b border-gray-75 bg-gray-50 px-3 py-2">
                <span />
                <ColumnLabel>Project</ColumnLabel>
                <ColumnLabel align="right">Configs</ColumnLabel>
                <ColumnLabel align="right">Secrets</ColumnLabel>
                <span />
              </div>

              {/* Scrolls rather than truncating: a row you can only reach by
                  searching is a row you cannot tick by browsing. Rows keep their
                  alphabetical place when ticked, so the one under the pointer
                  never moves. */}
              <div className="max-h-[420px] overflow-y-auto">
                {visible.map((p) => {
                  const locked = earlier.has(p.slug);
                  const on = locked || selected.has(p.slug);
                  return (
                    <label
                      key={p.slug}
                      className={`grid grid-cols-[22px_1fr_64px_76px_128px] items-center gap-2.5 border-b border-gray-75 px-3 py-2 text-[13px] last:border-b-0 ${
                        locked ? 'bg-gray-25' : on ? 'cursor-pointer bg-purple-50' : 'cursor-pointer'
                      }`}
                    >
                      <Checkbox
                        checked={on}
                        disabled={locked}
                        onChange={() => toggle(p.slug)}
                        aria-label={p.name}
                      />
                      <span className="min-w-0">
                        <b className="block truncate font-semibold text-gray-900">{p.name}</b>
                        <span className="block truncate font-mono text-[11px] text-gray-500">{p.slug}</span>
                      </span>
                      <span className="text-right font-mono text-xs tabular-nums text-gray-600">{p.configs}</span>
                      <span className="text-right font-mono text-xs tabular-nums text-gray-600">
                        {p.secrets.toLocaleString()}
                      </span>
                      <span className="text-right">
                        {locked && p.migrated_at && <Pill tone="done">Migrated {formatDate(p.migrated_at)}</Pill>}
                      </span>
                    </label>
                  );
                })}
                {visible.length === 0 && (
                  <p className="px-3 py-6 text-center text-[13px] text-gray-500">No project matches "{query}".</p>
                )}
              </div>

              <div className="flex items-center gap-2.5 border-t border-gray-75 bg-gray-25 px-3 py-2.5 text-[12.5px] text-gray-700">
                {preview ? (
                  <span>
                    <b className="text-gray-900">
                      {preview.projects.length} of {total} {plural(total, 'project')}
                    </b>{' '}
                    · <span className="tabular-nums">{preview.configs.toLocaleString()}</span> configs ·{' '}
                    <span className="tabular-nums">{preview.secrets.toLocaleString()}</span> secrets
                    {earlier.size > 0 && ` · ${earlier.size} already migrated, brought up to date in the same run`}
                  </span>
                ) : (
                  <span className="text-gray-500">No projects selected</span>
                )}
              </div>
            </Block>

            <DependencyPanel preview={preview} />
          </div>
        )}

        {mode === 'partial' && preview && <PartialWarnings preview={preview} />}

        <FooterBar
          left={
            <Button variant="ghost" onClick={() => navigate('/gathering')}>
              Back
            </Button>
          }
          right={
            <Button size="md" onClick={proceed} disabled={!canContinue || saving}>
              {saving ? 'Saving…' : 'Continue'} <ArrowRight size={14} />
            </Button>
          }
        />
      </div>
    </div>
  );
}

function ModeCard({
  id,
  checked,
  onSelect,
  title,
  description,
  points,
}: {
  id: string;
  checked: boolean;
  onSelect: () => void;
  title: string;
  description: string;
  points: { tone: 'yes' | 'no' | 'update'; text: string }[];
}) {
  return (
    <label
      htmlFor={id}
      className={`flex cursor-pointer items-start gap-3 rounded-md border px-4 py-3.5 ${
        checked ? 'border-purple-500 bg-purple-50' : 'border-gray-75 bg-gray-0 hover:border-gray-100'
      }`}
    >
      <Radio id={id} name="scope-mode" checked={checked} onChange={onSelect} className="mt-0.5" />
      <span className="min-w-0">
        <b className="block text-sm font-semibold text-gray-900">{title}</b>
        <span className="mt-0.5 block text-[12.5px] leading-normal text-gray-600">{description}</span>
        <ul className="mt-2 flex flex-col gap-1">
          {points.map((p) => (
            <li key={p.text} className="flex items-baseline gap-2 text-xs text-gray-600">
              {p.tone === 'yes' && <Check size={11} className="shrink-0 text-green-500" />}
              {p.tone === 'no' && <Minus size={11} className="shrink-0 text-gray-400" />}
              {p.tone === 'update' && <RotateCw size={11} className="shrink-0 text-purple-500" />}
              {p.text}
            </li>
          ))}
        </ul>
      </span>
    </label>
  );
}

const KIND_ICONS: Record<IncludedResource['kind'], typeof Users> = {
  group: Users,
  service_account: Bot,
  project_role: ShieldCheck,
  workplace_role: ShieldCheck,
};

const KIND_LABELS: Record<IncludedResource['kind'], [string, string]> = {
  group: ['group', 'groups'],
  service_account: ['service account', 'service accounts'],
  project_role: ['project role', 'project roles'],
  workplace_role: ['workplace role', 'workplace roles'],
};

/**
 * What the selection brings along, and why. The reason matters as much as the
 * name: "why is this here" is the first thing a cautious tester asks.
 */
function DependencyPanel({ preview }: { preview: ScopeResolution | null }) {
  if (!preview) {
    return (
      <Block className="px-3.5 py-3">
        <SideLabel>Also Coming Across</SideLabel>
        <p className="mt-1.5 text-[12.5px] text-gray-500">Pick a project to see what it depends on.</p>
      </Block>
    );
  }

  const byKind = new Map<IncludedResource['kind'], IncludedResource[]>();
  for (const res of preview.included ?? []) {
    byKind.set(res.kind, [...(byKind.get(res.kind) ?? []), res]);
  }

  const deferredWorkplace = [
    preview.deferred_groups > 0 && `${preview.deferred_groups} ${plural(preview.deferred_groups, 'group')}`,
    preview.deferred_roles > 0 && `${preview.deferred_roles} ${plural(preview.deferred_roles, 'role')}`,
    preview.deferred_service_accounts > 0 &&
      `${preview.deferred_service_accounts} ${plural(preview.deferred_service_accounts, 'service account')}`,
  ].filter(Boolean) as string[];

  return (
    <Block className="flex flex-col py-2">
      <div className="px-3.5 pb-1 pt-1.5">
        <SideLabel>Also Coming Across</SideLabel>
      </div>
      {byKind.size === 0 ? (
        <p className="px-3.5 py-1.5 text-[12.5px] text-gray-500">
          Nothing — the selected projects' members use only built-in roles and users.
        </p>
      ) : (
        [...byKind.entries()].map(([kind, items]) => {
          const Icon = KIND_ICONS[kind];
          const [one, many] = KIND_LABELS[kind];
          return (
            <div key={kind} className="flex items-baseline gap-2 px-3.5 py-1.5 text-[12.5px] text-gray-700">
              <Icon size={12} className="shrink-0 translate-y-0.5 text-purple-500" />
              <span className="min-w-0">
                {items.length} {items.length === 1 ? one : many}
                <span className="block text-[11.5px] text-gray-500" title={items.map((i) => `${i.name}: ${i.reasons.join('; ')}`).join('\n')}>
                  {items.map((i) => i.name).join(', ')}
                </span>
              </span>
            </div>
          );
        })
      )}

      <div className="my-1.5 h-px bg-gray-75" />
      <div className="px-3.5 pb-1">
        <SideLabel>Left For The Full Run</SideLabel>
      </div>
      <SideRow>
        {preview.deferred_projects} other {plural(preview.deferred_projects, 'project')}
      </SideRow>
      {preview.defers_workplace_settings && <SideRow>Workplace settings</SideRow>}
      {preview.deferred_policies > 0 && (
        <SideRow>
          {preview.deferred_policies} change-request {plural(preview.deferred_policies, 'policy', 'policies')}
        </SideRow>
      )}
      {deferredWorkplace.length > 0 && <SideRow>{deferredWorkplace.join(', ')} nothing selected uses</SideRow>}
    </Block>
  );
}

function PartialWarnings({ preview }: { preview: ScopeResolution }) {
  const policies = preview.policies_affected ?? [];
  const added = preview.added ?? [];
  if (policies.length === 0 && added.length === 0) return null;

  return (
    <div className="flex flex-col gap-2.5">
      {policies.length > 0 && (
        <Callout tone="warning">
          <span className="font-semibold text-gray-900">Change requests will not be required on the target yet.</span>{' '}
          {policies.length === 1 ? `The policy ${policies[0]} covers` : `${policies.length} policies cover`} selected
          projects on the source. Policies are workplace resources, so they wait for the full run — until then,
          changes to these projects on the target go through without review.
        </Callout>
      )}
      {added.map((a) => (
        <Callout key={a.project} tone="info">
          <span className="font-semibold text-gray-900">{a.project} was added to keep an inheritance intact.</span>{' '}
          {a.because}, and {a.project} was not selected. Without it the config would come across without its parent.
        </Callout>
      ))}
    </div>
  );
}

function ColumnLabel({ children, align }: { children: React.ReactNode; align?: 'right' }) {
  return (
    <span
      className={`text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400 ${
        align === 'right' ? 'text-right' : ''
      }`}
    >
      {children}
    </span>
  );
}

function SideLabel({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">{children}</span>
  );
}

function SideRow({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-2 px-3.5 py-1.5 text-[12.5px] text-gray-700">
      <Minus size={11} className="shrink-0 text-gray-400" />
      <span>{children}</span>
    </div>
  );
}

function plural(n: number, one: string, many = `${one}s`): string {
  return n === 1 ? one : many;
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}
