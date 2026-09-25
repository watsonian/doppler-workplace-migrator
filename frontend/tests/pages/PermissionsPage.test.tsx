import { spawnSync } from 'node:child_process';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import PermissionsPage, { apiScript } from '../../src/pages/PermissionsPage';

function renderPage() {
  return render(
    <MemoryRouter>
      <PermissionsPage />
    </MemoryRouter>,
  );
}

/** The JSON bodies passed with -d '…' in the script. */
function bodies(script: string): unknown[] {
  return [...script.matchAll(/-d '(\{[\s\S]*?\})'/g)].map((m) => JSON.parse(m[1]));
}

describe('PermissionsPage', () => {
  it('lists the permissions, with the manage ones marked', () => {
    renderPage();
    expect(screen.getByText('View Secrets')).toBeInTheDocument();
    expect(screen.getByText('enclave_project_config_secrets_read')).toBeInTheDocument();
    expect(screen.getAllByText('Manage')).toHaveLength(4);
    // Access All Projects is part of the setup, not an extra.
    expect(screen.getByText('Access All Projects')).toBeInTheDocument();
    expect(screen.queryByText('Optional')).not.toBeInTheDocument();
  });

  it('warns that a default project role reaches far more than the account', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole('radio', { name: /Default Project Role/ }));
    expect(screen.getByText(/Not recommended: it hands this role to far more than the account/)).toBeInTheDocument();
  });

  it('offers three ways to reach every project, and rules out Admin for read-only', async () => {
    const user = userEvent.setup();
    renderPage();
    // Adding it to each project is the default: the other two reach further.
    expect(screen.getByRole('radio', { name: /Add It To Each Project/ })).toBeChecked();

    await user.click(screen.getByRole('radio', { name: /Admin on All Projects/ }));
    expect(screen.getByText(/No project role is needed/)).toBeInTheDocument();
    // Says plainly what it can change on the source.
    expect(screen.getByText('This gives the account write access to the source.')).toBeInTheDocument();
    expect(screen.getByText(/add, edit and delete secrets and their notes/)).toBeInTheDocument();
    expect(screen.getByText(/rename and delete projects/)).toBeInTheDocument();

    await user.click(screen.getByRole('radio', { name: /Read-Only/ }));
    expect(screen.getByRole('radio', { name: /Admin on All Projects/ })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /Add It To Each Project/ })).toBeChecked();
  });

  it('shows what a read-only token leaves out', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole('radio', { name: /Read-Only/ }));

    expect(screen.getAllByText('Left out')).toHaveLength(4);
    expect(screen.getByText('Webhooks are not migrated.')).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /Read-Only/ })).toBeChecked();
  });
});

describe('the API script', () => {
  it('sends valid JSON in every request', () => {
    for (const readOnly of [false, true]) {
      expect(bodies(apiScript(readOnly, 'default_role'))).toHaveLength(3); // role, account, token
      expect(bodies(apiScript(readOnly, 'each_project'))).toHaveLength(3);
    }
    expect(bodies(apiScript(false, 'admin_all'))).toHaveLength(2); // account, token
  });

  it('gives the full setup every permission, with dependencies', () => {
    const [role, account] = bodies(apiScript(false)) as [
      { name: string; permissions: string[] },
      { workplace_role: { permissions: string[] } },
    ];
    expect(role.name).toBe('Migrator Source');
    expect(role.permissions).toEqual(
      expect.arrayContaining([
        'enclave_project_environment_list_all', // required by environment_all
        'enclave_project_environment_all',
        'enclave_project_config_secrets_read', // required by the three below
        'enclave_project_config_rotated_secrets_read',
        'enclave_project_config_service_tokens',
        'enclave_project_config_trusted_ips',
        'enclave_project_members',
        'enclave_project_webhooks',
      ]),
    );
    // service_accounts needs team; the two service account views need service_accounts.
    expect(account.workplace_role.permissions).toEqual([
      'team',
      'service_accounts',
      'service_account_api_tokens',
      'service_account_identities',
      'change_request_policy_read',
      'workplace_integrations_list',
      'all_enclave_projects',
    ]);
  });

  it('leaves the manage permissions out of the read-only setup', () => {
    const [role] = bodies(apiScript(true)) as [{ permissions: string[] }];
    expect(role.permissions).not.toContain('enclave_project_webhooks');
    expect(role.permissions).not.toContain('enclave_project_members');
    expect(role.permissions).not.toContain('enclave_project_config_service_tokens');
    expect(role.permissions).not.toContain('enclave_project_config_trusted_ips');
    expect(role.permissions).toContain('enclave_project_config_secrets_read');
  });

  it('never writes the admin token to disk, and says how to supply it', () => {
    const script = apiScript(false);
    expect(script).not.toMatch(/DOPPLER_TOKEN=["']/);
    expect(script).toContain('export DOPPLER_TOKEN=$(doppler configure get token --plain)');
    // Stops with a plain message rather than sending an empty token.
    expect(script).toContain('if [ -z "$DOPPLER_TOKEN" ]; then');
  });

  it('reads as plain shell, without shorthand only a Bash expert would know', () => {
    const script = apiScript(false);
    expect(script).not.toContain(':?'); // ${VAR:?message}
    expect(script).not.toMatch(/while :/);
    expect(script).not.toContain('[@]'); // arrays
    expect(script).not.toContain('&&');
    expect(script).not.toContain('\\"'); // JSON built from escaped quotes
  });

  it('is a script bash can parse, whichever setup and access', () => {
    for (const readOnly of [false, true]) {
      for (const access of ['default_role', 'admin_all', 'each_project'] as const) {
        const result = spawnSync('bash', ['-n'], { input: apiScript(readOnly, access), encoding: 'utf8' });
        expect(result.stderr).toBe('');
        expect(result.status).toBe(0);
      }
    }
  });

  it('adds the account to projects one by one only when asked to', () => {
    // A workplace with a thousand projects should not need a thousand requests.
    expect(apiScript(false, 'default_role')).not.toContain('/projects/project/members');
    expect(apiScript(false, 'admin_all')).not.toContain('/projects/project/members');
    expect(apiScript(false, 'each_project')).toContain('/projects/project/members');
  });

  it('leaves the default project role to the dashboard, and says so', () => {
    const script = apiScript(false, 'default_role');
    expect(script).toContain('Team > Roles > Default Roles');
    expect(script).toContain('set Project Role to "Migrator Source"');
  });

  it('gives Admin on All Projects instead of a project role, for a full token only', () => {
    const [account] = bodies(apiScript(false, 'admin_all')) as [{ workplace_role: { permissions: string[] } }];
    expect(account.workplace_role.permissions).toContain('all_enclave_projects_admin');
    expect(apiScript(false, 'admin_all')).not.toContain('/projects/roles');
    // Asked for with a read-only token, it is not given: that would be write access.
    expect(apiScript(true, 'admin_all')).not.toContain('all_enclave_projects_admin');
  });

  it('uses the role identifier Doppler derives from the name', () => {
    expect(apiScript(false, 'each_project')).toContain('role: "migrator_source"');
  });
});
