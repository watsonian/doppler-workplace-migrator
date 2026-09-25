import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowRight, Check, History, KeyRound, Minus, UserCog, Users } from 'lucide-react';
import {
  Block,
  BlockHeader,
  Button,
  FooterBar,
  PageHeading,
  Pill,
} from '@/components/ui/primitives';
import { getCheckpoint } from '@/api/client';
import { SCIM_DOCS_URL } from '@/lib/links';
import { formatWhen } from '@/lib/when';
import { useStandby } from '@/hooks/useStandby';
import type { CheckpointData, Standby } from '@/api/client';

/**
 * Everything the migrator moves. Listed exhaustively and in three groups,
 * because the useful question before a migration is not "does it work" but
 * "what will I still have to do afterwards" — and that answer is the second and
 * third lists, not the first.
 */
const MIGRATED = [
  'Workplace settings and custom roles',
  'Groups, their memberships and their roles',
  'Service accounts, their roles and OIDC identities',
  'Projects and environments, in source order',
  'Configs, including branch configs and inheritable flags',
  'Config inheritance relationships',
  'Secrets, with their visibility, value types and notes',
  'Config locks',
  'Webhooks, without their signing secrets',
  'Workplace user access levels and project member access',
  'Change request policies',
  'Trusted IP ranges, applied last',
];

const BY_HAND = [
  'Re-enter webhook signing secrets — the API never returns them, so migrated webhooks arrive unsigned',
  'Reissue service tokens and service account tokens — their values are write-once',
  'Recreate integrations and secrets syncs — credentials are redacted by the API',
  'Recreate rotated and dynamic secrets — each needs its integration and credentials',
];

const NOT_MIGRATED = [
  'Users — the API has no create-user endpoint, so provision them with SCIM first',
  'Personal configs — each belongs to one user and is invisible to any other token',
  'The workplace name — off unless you enable it on the Configuration step',
  'Change requests in flight, activity logs and dynamic secret leases',
];

export default function WelcomePage() {
  const navigate = useNavigate();
  const [checkpoint, setCheckpoint] = useState<CheckpointData | null>(null);
  const { standby } = useStandby();

  // A checkpoint from a run that stopped is the most useful thing this screen
  // can offer, so it is looked for before anything else is read.
  useEffect(() => {
    getCheckpoint()
      .then((data) => {
        if (data && data.started_at && !data.completed) setCheckpoint(data);
      })
      .catch(() => {
        // No checkpoint is the ordinary case, not an error.
      });
  }, []);

  // Partial runs that have finished. A full migration waiting to cut over
  // never gets here (the index opens the waiting page), so this is the one
  // that migrated some projects and has more to do. The tokens live in memory
  // only, so after a restart carrying on starts at Configure, with the
  // migration's instances filled in.
  const carryOn = standby?.unfinished && !standby.in_flight ? standby : null;
  const carryOnTo = carryOn?.configured ? '/scope' : '/configure';

  return (
    <div className="mx-auto w-full max-w-[860px] px-6 py-6">
      {checkpoint ? (
        <ResumeBanner checkpoint={checkpoint} onDiscard={() => setCheckpoint(null)} />
      ) : (
        carryOn && <CarryOnBanner standby={carryOn} onContinue={() => navigate(carryOnTo)} />
      )}

      <PageHeading
        title="Migrate A Doppler Workplace"
        subtitle="Copies one Doppler workplace into another, each on Doppler.com or self-hosted. Nothing on the source is changed."
      />

      {/* The two things to have ready, up front: both have to be done outside
          this tool before it can run, and finding that out at Configure or
          Pre-flight means stopping halfway. */}
      <Block className="mt-5">
        <BlockHeader title="Before You Start" />
        <div className="grid md:grid-cols-2">
          <div className="flex flex-col gap-2 border-b border-gray-75 px-3.5 py-3 md:border-b-0 md:border-r">
            <span className="flex items-center gap-2 text-[13px] font-semibold text-gray-900">
              <KeyRound size={14} className="text-purple-500" /> A service account on the source
            </span>
            <p className="text-[12.5px] text-gray-600">
              Its token is the source token. Only a service account can read restricted secrets, and
              it needs specific permissions — read-only is possible, with some things left out.
            </p>
            <span>
              <Button variant="secondary" onClick={() => navigate('/permissions')}>
                Source Token Permissions <ArrowRight size={13} />
              </Button>
            </span>
          </div>
          <div className="flex flex-col gap-2 px-3.5 py-3">
            <span className="flex items-center gap-2 text-[13px] font-semibold text-gray-900">
              <Users size={14} className="text-purple-500" /> Users on the target
            </span>
            <p className="text-[12.5px] text-gray-600">
              Members, group memberships and change request reviewers can only be mapped to users
              who already exist there.{' '}
              <a
                href={SCIM_DOCS_URL}
                target="_blank"
                rel="noopener noreferrer"
                className="font-medium text-purple-500 hover:underline"
              >
                Set up SCIM
              </a>{' '}
              first — pre-flight says how many are missing. The target token needs workplace admin.
            </p>
          </div>
        </div>
      </Block>

      <div className="mt-5 grid gap-3.5 md:grid-cols-2">
        <Block>
          <BlockHeader title="Migrated Automatically">
            <Pill tone="done">{MIGRATED.length}</Pill>
          </BlockHeader>
          <ul className="flex flex-col gap-1.5 px-3.5 py-3">
            {MIGRATED.map((item) => (
              <li key={item} className="flex items-start gap-2.5 text-[12.5px] text-gray-700">
                <Check size={13} className="mt-0.5 shrink-0 text-green-500" />
                {item}
              </li>
            ))}
          </ul>
        </Block>

        <div className="flex flex-col gap-3.5">
          <Block>
            <BlockHeader title="You Will Do By Hand">
              <Pill tone="warning">{BY_HAND.length}</Pill>
            </BlockHeader>
            <ul className="flex flex-col gap-1.5 px-3.5 py-3">
              {BY_HAND.map((item) => (
                <li key={item} className="flex items-start gap-2.5 text-[12.5px] text-gray-700">
                  <UserCog size={13} className="mt-0.5 shrink-0 text-yellow-500" />
                  {item}
                </li>
              ))}
            </ul>
          </Block>

          <Block>
            <BlockHeader title="Not Migrated" />
            <ul className="flex flex-col gap-1.5 px-3.5 py-3">
              {NOT_MIGRATED.map((item) => (
                <li key={item} className="flex items-start gap-2.5 text-[12.5px] text-gray-500">
                  <Minus size={13} className="mt-0.5 shrink-0 text-gray-400" />
                  {item}
                </li>
              ))}
            </ul>
          </Block>
        </div>
      </div>


      <FooterBar
        left={
          <span className="text-[12.5px] text-gray-500">
            Nothing is written to the target until you confirm on the Review step.
          </span>
        }
        right={
          carryOn ? (
            <Button size="md" onClick={() => navigate(carryOnTo)}>
              Continue Migration <ArrowRight size={14} />
            </Button>
          ) : (
            <Button size="md" onClick={() => navigate('/configure')}>
              Get Started <ArrowRight size={14} />
            </Button>
          )
        }
      />
    </div>
  );
}

/**
 * A run that stopped is resumable, and saying so here saves someone starting
 * again and wondering why the target already has half a workplace in it.
 */
function ResumeBanner({
  checkpoint,
  onDiscard,
}: {
  checkpoint: CheckpointData;
  onDiscard: () => void;
}) {
  const navigate = useNavigate();
  const phases = Object.values(checkpoint.phases ?? {});
  const done = phases.filter((p) => p.status === 'completed').length;
  const inProgress = phases.find((p) => p.status === 'in_progress');

  return (
    <div className="mb-5 flex flex-wrap items-center gap-3 rounded-md border border-yellow-100 bg-yellow-50 px-4 py-3 text-[13px] text-gray-800">
      <History size={16} className="shrink-0 text-yellow-500" />
      <span className="min-w-0">
        A run from <b className="font-semibold">{formatTime(checkpoint.started_at)}</b> stopped
        {done > 0 && <> after {done} of 10 phases</>}
        {inProgress && <> during {inProgress.name.replace(/_/g, ' ')}</>}.
        {checkpoint.error && <> {checkpoint.error}</>}
      </span>
      <span className="ml-auto flex gap-2">
        <Button variant="secondary" onClick={onDiscard}>
          Start Fresh
        </Button>
        <Button onClick={() => navigate('/migrate')}>Resume Run</Button>
      </span>
    </div>
  );
}

/**
 * Partial runs have already put some projects on the target. Only one
 * migration can be in progress, so the way on is to carry this one on:
 * more projects, or the full run.
 */
function CarryOnBanner({ standby, onContinue }: { standby: Standby; onContinue: () => void }) {
  const projects = standby.projects_migrated ?? 0;
  const runs = standby.runs.length;
  return (
    <div className="mb-5 flex flex-wrap items-center gap-3 rounded-md border border-blue-100 bg-blue-50 px-4 py-3 text-[13px] text-gray-800">
      <History size={16} className="shrink-0 text-blue-500" />
      <span className="min-w-0">
        A migration
        {standby.target_host && (
          <>
            {' '}
            to <b className="font-semibold">{standby.target_host}</b>
          </>
        )}{' '}
        is in progress: {projects} {projects === 1 ? 'project' : 'projects'} migrated in {runs}{' '}
        {runs === 1 ? 'run' : 'runs'}
        {standby.last_synced_at && <>, most recently {formatWhen(standby.last_synced_at)}</>}.
      </span>
      <span className="ml-auto">
        <Button onClick={onContinue}>
          Continue Migration <ArrowRight size={13} />
        </Button>
      </span>
    </div>
  );
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
