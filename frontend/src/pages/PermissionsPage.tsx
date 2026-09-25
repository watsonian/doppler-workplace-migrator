import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowLeft, Check, Copy, Minus } from 'lucide-react';
import { Block, BlockHeader, Button, Callout, PageHeading, Pill, Radio } from '@/components/ui/primitives';
import { highlightShell } from '@/lib/highlightShell';

/**
 * Source Token Permissions: what the migrator reads from the source, and how
 * to create a service account that can read it — in the dashboard and with
 * the API.
 *
 * Everything here is from Doppler's own permission checks, route by route.
 * Two setups: full, which reads everything, and read-only, which cannot read
 * the four things Doppler gates behind "manage" permissions. The migrator
 * never writes to the source either way; the choice is about which
 * permissions the account holds.
 */

type Setup = 'full' | 'read_only';

interface Permission {
  id: string;
  name: string;
  scope: 'Workplace' | 'Project';
  why: string;
  /** Only a "manage" permission can read this; left out of the read-only setup. */
  manage?: boolean;
  /** What a read-only setup loses without it. */
  without?: string;
}

// UI names are Doppler's own labels for each permission, as the dashboard shows
// them; identifiers are what the API takes.
const WORKPLACE: Permission[] = [
  { id: 'team', name: 'View Team', scope: 'Workplace', why: 'Users, groups and their members, and project members' },
  { id: 'service_accounts', name: 'View Service Accounts', scope: 'Workplace', why: 'Service accounts, and service accounts as project members' },
  { id: 'service_account_api_tokens', name: 'View Service Account API Tokens', scope: 'Workplace', why: 'Service account tokens, for the token worklist' },
  { id: 'service_account_identities', name: 'View Service Account Identities', scope: 'Workplace', why: 'OIDC identities of service accounts' },
  { id: 'change_request_policy_read', name: 'View Change Request Policies', scope: 'Workplace', why: 'Change request policies' },
  { id: 'workplace_integrations_list', name: 'View All Integration Connections', scope: 'Workplace', why: 'Integrations, for the manual steps' },
  {
    id: 'all_enclave_projects',
    name: 'Access All Projects',
    scope: 'Workplace',
    why: 'Every project, including ones created after the account, with the workplace’s default project role',
  },
];

const PROJECT: Permission[] = [
  { id: 'enclave_project_environment_list_all', name: 'List All Environments', scope: 'Project', why: 'Every environment' },
  { id: 'enclave_project_environment_all', name: 'Access All Configs', scope: 'Project', why: 'Every config in every environment' },
  { id: 'enclave_project_config_secrets_read', name: 'View Secrets', scope: 'Project', why: 'Secrets, including restricted ones' },
  { id: 'enclave_project_config_rotated_secrets_read', name: 'View Rotated Secrets', scope: 'Project', why: 'Rotated secrets, for the manual steps' },
  {
    id: 'enclave_project_config_service_tokens',
    name: 'Manage Service Tokens',
    scope: 'Project',
    why: 'Service tokens, for the token worklist',
    manage: true,
    without: 'Service tokens are not listed, so none can be issued on the target.',
  },
  {
    id: 'enclave_project_config_trusted_ips',
    name: 'Manage Trusted IPs',
    scope: 'Project',
    why: 'Trusted IP ranges, which cutover mirrors',
    manage: true,
    without: 'Trusted IP ranges are not mirrored; the target keeps its defaults.',
  },
  {
    id: 'enclave_project_members',
    name: 'Manage Project Members',
    scope: 'Project',
    why: 'Who has access to each project',
    manage: true,
    without: 'Project members are not migrated; grant access on the target by hand.',
  },
  {
    id: 'enclave_project_webhooks',
    name: 'Manage Project Webhooks',
    scope: 'Project',
    why: 'Webhooks',
    manage: true,
    without: 'Webhooks are not migrated.',
  },
];

const ROLE_NAME = 'Migrator Source';
// Doppler derives a role's identifier from its name: lowercased, spaces to underscores.
const ROLE_ID = 'migrator_source';

/** The extra workplace permission the Admin on All Projects route adds. */
const ADMIN_ALL: Permission = {
  id: 'all_enclave_projects_admin',
  name: 'Admin on All Projects',
  scope: 'Workplace',
  why: 'Admin on every project, current and future — covers every project permission, and writing too',
};

/**
 * What Doppler's built-in Admin project role can change, from its permission
 * list (AdminPermissions), grouped the way the dashboard groups them. Reading
 * is left out: the setup reads all of this anyway.
 */
const ADMIN_WRITES: { area: string; what: string }[] = [
  {
    area: 'Secrets',
    what: 'add, edit and delete secrets and their notes, roll back config changes, manage rotated and dynamic secrets and lease dynamic secret values, and manage secret reminders',
  },
  {
    area: 'Configs',
    what: 'create, duplicate, rename, lock and delete configs, change inheritance and secrets referencing, and add, edit or delete syncs',
  },
  { area: 'Environments', what: 'create, rename, reorder and delete environments, and change their settings' },
  { area: 'Projects', what: 'rename and delete projects, manage their tags, and turn inheritance and secrets referencing on or off' },
  {
    area: 'Access',
    what: 'add and remove project members and groups, add and revoke service tokens, add and delete trusted IP ranges, add, edit and remove webhooks, approve change requests and assign change request policies',
  },
];

/**
 * How the account reaches every project. The first two need no per-project
 * work at all; the third adds the account to each project, one request each.
 */
type Access = 'default_role' | 'admin_all' | 'each_project';

export default function PermissionsPage() {
  const navigate = useNavigate();
  const [setup, setSetup] = useState<Setup>('full');
  const [chosenAccess, setAccess] = useState<Access>('each_project');
  const readOnly = setup === 'read_only';
  // Admin on All Projects is write access everywhere, so it is no way to get
  // a read-only token.
  const access: Access = readOnly && chosenAccess === 'admin_all' ? 'each_project' : chosenAccess;

  const projectPerms = PROJECT.filter((p) => !(readOnly && p.manage));
  const workplacePerms = access === 'admin_all' ? [...WORKPLACE, ADMIN_ALL] : WORKPLACE;

  return (
    <div className="mx-auto w-full max-w-[860px] px-6 py-6">
      <div className="mb-3">
        <Button variant="ghost" onClick={() => navigate(-1)}>
          <ArrowLeft size={14} /> Back
        </Button>
      </div>
      <PageHeading
        title="Source Token Permissions"
        subtitle="What the migrator reads from the source workplace, and how to create a service account that can read it."
      />

      <div className="mt-5 flex flex-col gap-3.5">
        <Callout tone="info">
          <span className="font-semibold text-gray-900">Use a service account.</span> Doppler lets
          only service accounts read the values of <span className="font-mono">restricted</span>{' '}
          secrets; a personal or CLI token gets nothing back for them. The migrator only ever reads
          from the source, and it does not migrate the service account its own token belongs to.
        </Callout>

        <Block>
          <BlockHeader title="Choose A Setup" />
          <div role="radiogroup" aria-label="Setup" className="grid gap-3 p-3.5 md:grid-cols-2">
            <SetupOption
              id="setup-full"
              name="setup"
              checked={!readOnly}
              onSelect={() => setSetup('full')}
              title="Full (Recommended)"
              description="Reads everything the migrator migrates. Four of the project permissions are Doppler's “manage” permissions — there is no read-only way to list those things — but the migrator never uses them to write."
            />
            <SetupOption
              id="setup-read-only"
              name="setup"
              checked={readOnly}
              onSelect={() => setSetup('read_only')}
              title="Read-Only"
              description="Only view permissions. The migration carries on without the four things Doppler gates behind manage permissions, and lists them as not migrated."
            />
          </div>
          {readOnly && (
            <div className="border-t border-gray-75 px-3.5 py-3">
              <p className="text-[12.5px] font-semibold text-gray-900">Left out with a read-only token</p>
              <ul className="mt-1.5 flex flex-col gap-1">
                {PROJECT.filter((p) => p.manage).map((p) => (
                  <li key={p.id} className="flex items-baseline gap-2 text-[12.5px] text-gray-700">
                    <Minus size={12} className="shrink-0 text-gray-400" />
                    {p.without}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </Block>

        <Block>
          <BlockHeader title="Choose How It Reaches Every Project" />
          <div role="radiogroup" aria-label="Project access" className="grid gap-3 p-3.5 md:grid-cols-3">
            <SetupOption
              id="access-each-project"
              name="access"
              checked={access === 'each_project'}
              onSelect={() => setAccess('each_project')}
              title="Add It To Each Project (Recommended)"
              description={`Gives ${ROLE_NAME} to this account alone, project by project. One request per project — a while on a workplace with hundreds — and projects created later need adding too.`}
            />
            <SetupOption
              id="access-admin-all"
              name="access"
              checked={access === 'admin_all'}
              onSelect={() => setAccess('admin_all')}
              disabled={readOnly}
              title="Admin on All Projects"
              description={
                readOnly
                  ? 'Not with a read-only token: it is admin on every project, which includes writing.'
                  : 'No project role and no per-project step. But it is admin on every project — writing secrets, renaming and deleting projects — well beyond what the migrator needs.'
              }
            />
            <SetupOption
              id="access-default-role"
              name="access"
              checked={access === 'default_role'}
              onSelect={() => setAccess('default_role')}
              title="Default Project Role"
              description={`Make ${ROLE_NAME} the workplace’s default project role, so no per-project step is needed. But it then goes to everyone given project access without a role chosen, not only this account.`}
            />
          </div>
          {access === 'admin_all' && (
            <div className="border-t border-gray-75 bg-yellow-50 px-3.5 py-3 text-[12.5px] text-gray-700">
              <b className="text-gray-900">This gives the account write access to the source.</b>{' '}
              Admin on All Projects is Doppler’s built-in Admin project role on every project, current
              and future. The migrator never writes to the source, but anyone holding this token could:
              <ul className="mt-2 flex flex-col gap-1">
                {ADMIN_WRITES.map((w) => (
                  <li key={w.area} className="flex items-baseline gap-2">
                    <Minus size={12} className="shrink-0 text-yellow-600" />
                    <span>
                      <b className="font-semibold text-gray-900">{w.area}:</b> {w.what}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {access === 'default_role' && (
            <div className="border-t border-gray-75 bg-yellow-50 px-3.5 py-3 text-[12.5px] text-gray-700">
              <b className="text-gray-900">Not recommended: it hands this role to far more than the account.</b>{' '}
              The default project role is what anyone gets when they are given access to a project
              without a role being chosen — the dashboard’s <Ui>Add Members</Ui> does exactly that —
              and what everyone with Access All Projects gets on every project. With {ROLE_NAME} as the
              default, each of them could read every secret in every environment of those projects
              {readOnly ? '' : ', and manage their service tokens, members and webhooks'}. It also
              needs a plan with team access controls.
            </div>
          )}
        </Block>

        <PermissionTable title="Workplace Permissions" perms={workplacePerms} readOnly={readOnly} />
        {access === 'admin_all' ? (
          <Block>
            <BlockHeader title="Project Permissions" />
            <p className="px-3.5 py-3 text-[13px] text-gray-700">
              No project role is needed: Admin on All Projects gives the account the built-in{' '}
              <Ui>Admin</Ui> project role on every project, which includes all of them.
            </p>
          </Block>
        ) : (
          <PermissionTable title="Project Permissions" perms={PROJECT} readOnly={readOnly} />
        )}

        <Block>
          <BlockHeader title="In The Dashboard" />
          <ol className="flex list-decimal flex-col gap-3 py-3.5 pl-9 pr-3.5 text-[13px] leading-relaxed text-gray-700">
            {access !== 'admin_all' && (
              <li>
                <b className="text-gray-900">Create the project role.</b> Go to <Ui>Team</Ui> →{' '}
                <Ui>Roles</Ui>. In <Ui>Custom Project Roles</Ui>, click <Ui>+</Ui> (or{' '}
                <Ui>Create Custom Project Role</Ui>), name it <Ui>{ROLE_NAME}</Ui>, and click{' '}
                <Ui>Next</Ui>. Tick the {projectPerms.length} project permissions above, then click{' '}
                <Ui>Save</Ui>. Custom roles need a plan that includes them.
              </li>
            )}
            <li>
              <b className="text-gray-900">Create the service account.</b> Go to <Ui>Team</Ui> →{' '}
              <Ui>Service Accounts</Ui> and click <Ui>+</Ui>. Enter a name and click{' '}
              <Ui>Create Service Account</Ui>. It starts with no access.
            </li>
            <li>
              <b className="text-gray-900">Give it the workplace permissions.</b> On the service
              account’s page, in <Ui>Service Account Roles</Ui>, click the button beside{' '}
              <Ui>Workplace Role</Ui>. Under <Ui>Workplace Permissions</Ui>, tick the{' '}
              {workplacePerms.length} workplace permissions above, then click <Ui>Save</Ui>.
            </li>
            {access === 'default_role' && (
              <li>
                <b className="text-gray-900">Make the role the default.</b> Go to <Ui>Team</Ui> →{' '}
                <Ui>Roles</Ui>. In <Ui>Default Roles</Ui>, set <Ui>Project Role</Ui> to{' '}
                <Ui>{ROLE_NAME}</Ui> and click <Ui>Save</Ui>. The account now has it on every
                project, and on new ones as they are created — and so does everyone else given
                project access without a role, as the warning above says.
              </li>
            )}
            {access === 'each_project' && (
              <li>
                <b className="text-gray-900">Give it every project.</b> On the service account’s
                page, in <Ui>Project Access</Ui>, click <Ui>Add Project Access</Ui> (or the pencil).
                Tick every project, choose <Ui>{ROLE_NAME}</Ui> as the <Ui>Role</Ui>, and click{' '}
                <Ui>Save</Ui>. Do the same for projects created later.
              </li>
            )}
            <li>
              <b className="text-gray-900">Create its token.</b> In{' '}
              <Ui>Service Account API Tokens</Ui>, click <Ui>+</Ui>. Name it, set an expiry with{' '}
              <Ui>Expire Token</Ui> if you want one, and click <Ui>Create API Token</Ui>. Copy it —
              it is shown once. It is the source token, or <span className="font-mono">SOURCE_DOPPLER_TOKEN</span>.
            </li>
          </ol>
        </Block>

        <Block>
          <BlockHeader title="With The API" />
          <div className="flex flex-col gap-3 p-3.5">
            <p className="text-[13px] text-gray-700">
              A script that does the same. Save it as a file and run it with{' '}
              <span className="font-mono">bash</span>; it needs{' '}
              <span className="font-mono">curl</span> and <span className="font-mono">jq</span>. It
              runs with a token that can manage service accounts
              {access === 'admin_all' ? '' : ', roles and project members'} in the source workplace
              — a workplace admin’s, say — which is only used to set up the account and cannot grant
              it more than it has itself.
            </p>
            {access === 'default_role' && (
              <Callout tone="warning">
                Setting the default project role has no API, so the script stops short of it: do
                step 4 above in the dashboard once it has run.
              </Callout>
            )}
            {access === 'each_project' && (
              <Callout tone="warning">
                The script makes one request per project to add the account, which takes a while on
                a workplace with hundreds of projects, and covers only the projects that exist when
                it runs.
              </Callout>
            )}
            <CodeBlock label="Setup" code={apiScript(readOnly, access)} />
          </div>
        </Block>

        <Block>
          <BlockHeader title="Worth Knowing" />
          <ul className="flex flex-col gap-2 px-3.5 py-3 text-[13px] text-gray-700">
            <li>
              <b className="text-gray-900">Trusted IP ranges apply to the account too.</b> A source
              config that restricts access by IP refuses every read from a machine outside its ranges,
              whatever the account’s role. Run the migrator from an allowed address.
            </li>
            <li>
              <b className="text-gray-900">Some things depend on the plan.</b> Groups, service
              account identities, integrations, secrets rotation and change request policies need
              features the source workplace’s plan may not include. Where it doesn’t, there is
              nothing of them to migrate, and the migrator carries on.
            </li>
            <li>
              <b className="text-gray-900">Pre-flight checks it.</b> The{' '}
              <Ui>Source token can read everything migrated</Ui> check tries each of these reads and
              says which the token is refused, before anything is gathered.
            </li>
            <li>
              <b className="text-gray-900">The target token is different.</b> It writes, so it needs
              workplace admin on the target instance.
            </li>
          </ul>
        </Block>
      </div>
    </div>
  );
}

function SetupOption({
  id,
  name,
  checked,
  onSelect,
  title,
  description,
  disabled,
}: {
  id: string;
  name: string;
  checked: boolean;
  onSelect: () => void;
  title: string;
  description: string;
  disabled?: boolean;
}) {
  return (
    <label
      htmlFor={id}
      className={`flex items-start gap-3 rounded-md border px-4 py-3.5 ${
        disabled
          ? 'cursor-not-allowed border-gray-75 bg-gray-25 opacity-70'
          : checked
            ? 'cursor-pointer border-purple-500 bg-purple-50'
            : 'cursor-pointer border-gray-75 bg-gray-0 hover:border-gray-100'
      }`}
    >
      <Radio id={id} name={name} checked={checked} onChange={onSelect} disabled={disabled} className="mt-0.5" />
      <span className="min-w-0">
        <b className="block text-sm font-semibold text-gray-900">{title}</b>
        <span className="mt-0.5 block text-[12.5px] leading-normal text-gray-600">{description}</span>
      </span>
    </label>
  );
}

function PermissionTable({ title, perms, readOnly }: { title: string; perms: Permission[]; readOnly: boolean }) {
  // Sized to the longest identifier, which is never wrapped: a broken
  // identifier reads as two, and is copied wrong.
  const cols = 'grid-cols-[minmax(200px,1fr)_340px_minmax(200px,1.2fr)]';
  return (
    <Block>
      <BlockHeader title={title} />
      <div className="overflow-x-auto">
        <div className="min-w-[760px]">
          <div className={`grid ${cols} gap-3 border-b border-gray-75 bg-gray-50 px-3.5 py-2`}>
            {['Permission', 'Identifier', 'Needed for'].map((h) => (
              <span key={h} className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">
                {h}
              </span>
            ))}
          </div>
          {perms.map((p) => {
            const leftOut = readOnly && p.manage;
            return (
              <div
                key={p.id}
                className={`grid ${cols} items-baseline gap-3 border-b border-gray-75 px-3.5 py-2 text-[13px] last:border-b-0 ${
                  leftOut ? 'bg-gray-25' : ''
                }`}
              >
                <span className={`flex flex-wrap items-center gap-1.5 ${leftOut ? 'text-gray-400 line-through' : 'text-gray-900'}`}>
                  {p.name}
                  {p.manage && !leftOut && <Pill tone="warning">Manage</Pill>}
                  {leftOut && <Pill tone="skipped">Left out</Pill>}
                </span>
                <span className={`whitespace-nowrap font-mono text-xs ${leftOut ? 'text-gray-400' : 'text-gray-600'}`}>{p.id}</span>
                <span className={`text-[12.5px] ${leftOut ? 'text-gray-400' : 'text-gray-600'}`}>{p.why}</span>
              </div>
            );
          })}
        </div>
      </div>
    </Block>
  );
}

/** A label as it appears in Doppler's dashboard. */
function Ui({ children }: { children: React.ReactNode }) {
  return <span className="rounded-sm bg-gray-50 px-1 py-px font-medium text-gray-900">{children}</span>;
}

function CodeBlock({ label, code }: { label: string; code: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      // The text is on screen and selectable, which is the fallback.
    }
  }
  return (
    <div className="overflow-hidden rounded-md border border-gray-900 bg-gray-975">
      <div className="flex items-center justify-between border-b border-gray-900 bg-gray-950 px-3 py-1.5">
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400">{label}</span>
        <button
          type="button"
          onClick={copy}
          aria-label={`Copy ${label}`}
          className="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[12px] font-semibold text-gray-200 transition-colors hover:bg-gray-800 hover:text-gray-0"
        >
          {copied ? <Check size={13} /> : <Copy size={13} />}
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre className="overflow-x-auto px-3.5 py-3 font-mono text-[12px] leading-relaxed text-[#E4E6EA]">
        <code>{highlightShell(code)}</code>
      </pre>
    </div>
  );
}

/** The setup as a script, for the chosen permissions and project access. */
export function apiScript(readOnly: boolean, access: Access = 'each_project'): string {
  const adminAll = access === 'admin_all' && !readOnly;
  const workplace = (adminAll ? [...WORKPLACE, ADMIN_ALL] : WORKPLACE).map((p) => `"${p.id}"`);
  const project = PROJECT.filter((p) => !(readOnly && p.manage)).map((p) => `"${p.id}"`);
  const list = (items: string[], indent: number) => items.map((i) => `${' '.repeat(indent)}${i}`).join(',\n');

  const steps: string[] = [];
  let n = 1;

  steps.push(`#!/usr/bin/env bash
#
# Creates a service account for the migrator in the source workplace.
# Save this as create-migrator-account.sh and run: bash create-migrator-account.sh
#
# It needs DOPPLER_TOKEN: a token that can manage service accounts${adminAll ? '' : ', roles and\n# project members'} in the source workplace, used only for these commands. Set
# it in your shell before running, not in a file. To use your Doppler CLI's
# token:
#
#   export DOPPLER_TOKEN=$(doppler configure get token --plain)

if [ -z "$DOPPLER_TOKEN" ]; then
  echo "DOPPLER_TOKEN is not set. See the comment at the top of this script."
  exit 1
fi

API="https://api.doppler.com/v3"`);

  if (!adminAll) {
    steps.push(`# ${n++}. Create a project role that can read what the migrator reads.
#    Doppler names the role's identifier after it: ${ROLE_ID}
curl -sS -X POST "$API/projects/roles" \\
  -H "Authorization: Bearer $DOPPLER_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{
  "name": "${ROLE_NAME}",
  "permissions": [
${list(project, 4)}
  ]
}'`);
  }

  steps.push(`# ${n++}. Create the service account with its workplace permissions, and keep its
#    ID. Each permission's dependencies have to be listed too; these are.${
    adminAll ? '\n#    Admin on All Projects gives it the Admin role on every project.' : ''
  }
SA=$(curl -sS -X POST "$API/workplace/service_accounts" \\
  -H "Authorization: Bearer $DOPPLER_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{
  "name": "${ROLE_NAME}",
  "workplace_role": {
    "permissions": [
${list(workplace, 6)}
    ]
  }
}' | jq -r '.service_account.slug')

echo "Created service account $SA"`);

  if (access === 'each_project') {
    steps.push(`# ${n++}. Add the service account to every project with the role. This is one
#    request per project, so it takes a while on a large workplace, and it
#    covers only the projects that exist now. Projects are listed 100 at a
#    time: this asks for page 1, page 2, and so on until one comes back empty.
page=1
while true; do
  projects=$(curl -sS "$API/projects?page=$page&per_page=100" \\
    -H "Authorization: Bearer $DOPPLER_TOKEN" | jq -r '.projects[].slug')

  if [ -z "$projects" ]; then
    break
  fi

  for project in $projects; do
    body=$(jq -n --arg project "$project" --arg account "$SA" \\
      '{project: $project, type: "service_account", slug: $account, role: "${ROLE_ID}"}')

    if curl -sS --fail -o /dev/null -X POST "$API/projects/project/members" \\
      -H "Authorization: Bearer $DOPPLER_TOKEN" \\
      -H "Content-Type: application/json" \\
      -d "$body"; then
      echo "Added to $project"
    else
      echo "Could not add to $project"
    fi
  done

  page=$((page + 1))
done`);
  }

  steps.push(`# ${n++}. Create the service account's token and print it. It is shown only
#    once: this is the source token for the migrator.
#    For a token that expires, add "expires_at": "2026-12-31T00:00:00Z".
curl -sS -X POST "$API/workplace/service_accounts/service_account/$SA/tokens" \\
  -H "Authorization: Bearer $DOPPLER_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"name": "workplace-migrator"}' | jq -r '.api_key'`);

  if (access === 'default_role') {
    steps.push(`# Last, in the dashboard, since it has no API: Team > Roles > Default Roles,
# set Project Role to "${ROLE_NAME}" and save. Until then the account can see
# the list of projects, but not the configs or secrets in them.`);
  }

  return steps.join('\n\n') + '\n';
}
