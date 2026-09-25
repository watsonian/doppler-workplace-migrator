import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import PlanReviewPage from '../../src/pages/PlanReviewPage';
import * as api from '../../src/api/client';
import type { CompareResponse, Difference, DriftRecord } from '../../src/types/diff';

vi.mock('../../src/api/client', () => ({
  getCompare: vi.fn(),
  getPlan: vi.fn(),
  setRunOptions: vi.fn(() => Promise.resolve({ status: 'saved' })),
  getScope: vi.fn(() => new Promise(() => {})),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <PlanReviewPage />
    </MemoryRouter>,
  );
}

function diff(overrides: Partial<Difference>): Difference {
  return {
    kind: 'secret',
    disposition: 'create',
    project: 'payments',
    config: 'prd',
    name: 'API_KEY',
    scope: 'payments/prd',
    ...overrides,
  };
}

const DRIFT: DriftRecord = {
  project: 'payments',
  config: 'prd',
  scope: 'payments/prd',
  changed_at: '2026-09-18T09:04:00Z',
  changed_by: 'rosa.imani@acme.com',
  summary: 'Updated secret STRIPE_WEBHOOK_SECRET',
};

function compareResponse(overrides: Partial<CompareResponse> = {}): CompareResponse {
  return {
    status: 'complete',
    counts: { create: 0, update: 0, match: 0, orphan: 0, drift: 0 },
    target_empty: false,
    differences: [],
    matched: {},
    ...overrides,
  };
}

const EMPTY_PLAN = {
  summary: {
    project_count: 0, environment_count: 0, config_count: 0, secret_count: 0,
    role_count: 0, group_count: 0, webhook_count: 0, service_account_count: 0,
    trusted_ip_count: 0, change_request_policy_count: 0, manual_step_count: 0,
  },
  projects: [],
  workplace: {
    settings: true, workplace_roles: [], project_roles: [], groups: [],
    service_accounts: [], change_request_policies: [],
  },
  manual_steps: [],
  user_check: { required: null, existing: null, missing: null },
};

describe('PlanReviewPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getPlan as Mock).mockResolvedValue(EMPTY_PLAN);
    (api.getCompare as Mock).mockResolvedValue(compareResponse());
  });

  it('shows a loading state while it fetches', () => {
    (api.getCompare as Mock).mockImplementation(() => new Promise(() => {}));
    renderPage();
    expect(screen.getByText(/Loading the plan/)).toBeInTheDocument();
  });

  it('leads with what will change rather than what exists', async () => {
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 604, update: 112, match: 17696, orphan: 0, drift: 0 },
        differences: [diff({})],
      }),
    );

    renderPage();
    expect(await screen.findByText('What This Run Will Change')).toBeInTheDocument();
    // "Already current" is the number that makes a top-up trustworthy: it says
    // the run looked at everything and changed only what needed changing.
    expect(screen.getByText(/17,696 already current/)).toBeInTheDocument();
  });

  it('says nothing needs doing when the two already match', async () => {
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({ counts: { create: 0, update: 0, match: 18412, orphan: 0, drift: 0 } }),
    );

    renderPage();
    expect(await screen.findByText(/The target already matches the source/)).toBeInTheDocument();
  });

  it('opens a short rollup without being asked, and closes on request', async () => {
    // A list small enough to read at a glance is more useful open. A long one
    // stays shut so the page is still scannable.
    const user = userEvent.setup();
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 2, update: 0, match: 0, orphan: 0, drift: 0 },
        differences: [diff({ name: 'API_KEY' }), diff({ name: 'DB_URL' })],
      }),
    );

    renderPage();
    expect(await screen.findByText('API_KEY')).toBeInTheDocument();
    expect(screen.getByText('DB_URL')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Secrets/ }));
    await waitFor(() => expect(screen.queryByText('API_KEY')).not.toBeInTheDocument());
  });

  it('keeps a long rollup closed until asked', async () => {
    const user = userEvent.setup();
    const many = Array.from({ length: 12 }, (_, i) => diff({ name: `SECRET_${i}` }));
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 12, update: 0, match: 0, orphan: 0, drift: 0 },
        differences: many,
      }),
    );

    renderPage();
    await screen.findByText('What This Run Will Change');
    expect(screen.queryByText('SECRET_0')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Secrets/ }));
    expect(await screen.findByText('SECRET_0')).toBeInTheDocument();
    // Ten rows, then it stops and links out.
    expect(screen.queryByText('SECRET_11')).not.toBeInTheDocument();
    expect(screen.getByText('2 more')).toBeInTheDocument();
  });

  it('gives workplace resources and projects no location column', async () => {
    // Groups live nowhere in particular and a project is its own location, so
    // the column was blank for one and repeated the name for the other.
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 3, update: 0, match: 0, orphan: 0, drift: 0 },
        differences: [
          diff({ kind: 'group', name: 'API Developers', project: undefined, config: undefined, scope: undefined }),
          diff({ kind: 'group', name: 'Admins', project: undefined, config: undefined, scope: undefined }),
          diff({ kind: 'project', name: 'api', project: 'api', config: undefined, scope: 'api' }),
        ],
      }),
    );

    renderPage();
    await screen.findByText('API Developers');
    expect(screen.queryByText('Scope')).not.toBeInTheDocument();
    expect(screen.queryByText('Project')).not.toBeInTheDocument();
    // "api" appears once as the project, not twice.
    expect(screen.getAllByText('api')).toHaveLength(1);
  });

  it('places a secret by its config and a config by its project', async () => {
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 2, update: 0, match: 0, orphan: 0, drift: 0 },
        differences: [
          diff({ kind: 'secret', name: 'API_KEY', project: 'payments', config: 'prd', scope: 'payments/prd' }),
          diff({ kind: 'config', name: 'stg', project: 'payments', config: 'stg', scope: 'payments/stg' }),
        ],
      }),
    );

    renderPage();
    await screen.findByText('API_KEY');
    expect(screen.getByText('Config')).toBeInTheDocument();
    expect(screen.getByText('payments/prd')).toBeInTheDocument();
    expect(screen.getByText('Project')).toBeInTheDocument();
    // The config row says "payments", not "payments/stg" next to "stg".
    expect(screen.queryByText('payments/stg')).not.toBeInTheDocument();
  });

  it('names the secret in an update, not only its config', async () => {
    // Updates used to show only where the item lived, so two changed secrets
    // in one config read as the same row twice.
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 0, update: 2, match: 0, orphan: 0, drift: 0 },
        differences: [
          diff({ disposition: 'update', name: 'STRIPE_KEY', changes: [{ field: 'value', was: '', now: '' }] }),
          diff({ disposition: 'update', name: 'DB_URL', changes: [{ field: 'value', was: '', now: '' }] }),
        ],
      }),
    );

    renderPage();
    expect(await screen.findByText('STRIPE_KEY')).toBeInTheDocument();
    expect(screen.getByText('DB_URL')).toBeInTheDocument();
  });

  it('names what a secret differs in, never what it differs to', async () => {
    // The plan is read on screen, screenshotted and exported.
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 0, update: 1, match: 0, orphan: 0, drift: 0 },
        differences: [
          diff({
            disposition: 'update',
            name: 'STRIPE_KEY',
            changes: [{ field: 'value', was: '', now: '' }],
          }),
        ],
      }),
    );

    renderPage();
    const rendered = (await screen.findByText('What This Run Will Change')).ownerDocument.body
      .textContent!;
    expect(rendered).toContain('value');
    expect(rendered).not.toContain('sk_live');
  });

  it('defers deletions and offers them as an opt-in', async () => {
    // Opt-in, unlike the drift choice below. Getting those the wrong way round
    // is how a tool deletes something nobody asked it to.
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 0, update: 0, match: 0, orphan: 2, drift: 0 },
        differences: [
          diff({ disposition: 'orphan', name: 'LEGACY_KEY' }),
          diff({ disposition: 'orphan', kind: 'config', name: 'legacy', scope: 'tools/legacy' }),
        ],
      }),
    );

    renderPage();
    expect(await screen.findByText('Deferred To Cutover · 2')).toBeInTheDocument();
    expect(
      screen.getByText(/Left unticked, these happen at cutover instead/),
    ).toBeInTheDocument();

    const ticks = screen.getAllByRole('checkbox');
    ticks.forEach((tick) => expect(tick).not.toBeChecked());
  });

  it('keeps config deletion separate from secret deletion', async () => {
    // Deleting a config takes its secrets with it, which is a larger decision.
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 0, update: 0, match: 0, orphan: 2, drift: 0 },
        differences: [
          diff({ disposition: 'orphan', name: 'LEGACY_KEY' }),
          diff({ disposition: 'orphan', kind: 'config', name: 'legacy', scope: 'tools/legacy' }),
        ],
      }),
    );

    renderPage();
    expect(await screen.findByText(/Delete secrets removed from the source/)).toBeInTheDocument();
    expect(screen.getByText(/Delete configs removed from the source/)).toBeInTheDocument();
    expect(screen.getByText(/takes its secrets with it/)).toBeInTheDocument();
  });

  it('presents drift as a choice that defaults to overwriting', async () => {
    // Opt-out: these are overwritten unless you say otherwise, so the default
    // has to be stated rather than implied.
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 0, update: 0, match: 0, orphan: 0, drift: 1 },
        drift: [DRIFT],
      }),
    );

    renderPage();
    expect(await screen.findByText('Changed On The Target · 1')).toBeInTheDocument();

    const overwrite = screen.getByRole('radio', { name: /Overwrite with the source/ });
    expect(overwrite).toBeChecked();
    expect(screen.getByRole('radio', { name: /Keep the target/ })).not.toBeChecked();
  });

  it('shows who changed the target, which is what the decision turns on', async () => {
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 0, update: 0, match: 0, orphan: 0, drift: 1 },
        drift: [DRIFT],
      }),
    );

    renderPage();
    await screen.findByText('Changed On The Target · 1');
    expect(screen.getByText('rosa.imani@acme.com')).toBeInTheDocument();
  });

  it('distinguishes unchecked drift from no drift', async () => {
    // A token without log access must not read as a clean bill of health.
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        drift_unavailable: 'The target token cannot read config logs.',
      }),
    );

    renderPage();
    expect(
      await screen.findByText(/Changes made on the target could not be checked/),
    ).toBeInTheDocument();
  });

  it('warns that users missing on the target will be skipped', async () => {
    (api.getPlan as Mock).mockResolvedValue({
      ...EMPTY_PLAN,
      user_check: { required: null, existing: null, missing: ['a@acme.com', 'b@acme.com'] },
    });

    renderPage();
    expect(await screen.findByText(/2 users are missing on the target/)).toBeInTheDocument();
    // With the way to fix it: Doppler's SCIM guide, in a new tab.
    const scim = screen.getByRole('link', { name: 'SCIM' });
    expect(scim).toHaveAttribute('href', 'https://docs.doppler.com/docs/scim');
    expect(scim).toHaveAttribute('target', '_blank');
  });

  it('carries the choices forward when continuing', async () => {
    const user = userEvent.setup();
    (api.getCompare as Mock).mockResolvedValue(
      compareResponse({
        counts: { create: 0, update: 0, match: 0, orphan: 1, drift: 0 },
        differences: [diff({ disposition: 'orphan', name: 'LEGACY_KEY' })],
      }),
    );

    renderPage();
    await user.click(await screen.findByRole('checkbox'));
    await user.click(screen.getByRole('button', { name: /^Continue/ }));

    // To the server, where the run reads them. This test used to assert the
    // choices rode along as navigation state — which it did, to a page that
    // never read them, so every run went ahead on defaults.
    await waitFor(() =>
      expect(api.setRunOptions).toHaveBeenCalledWith({
        delete_orphans: true,
        delete_orphan_configs: false,
        drift: 'overwrite',
      }),
    );
    // Straight to the run: the manual steps are a checklist for afterwards now.
    expect(mockedNavigate).toHaveBeenCalledWith('/migrate');
  });

  it('stays put and says so if the choices cannot be saved', async () => {
    // Moving on anyway would start a run on defaults the person did not pick.
    const user = userEvent.setup();
    (api.setRunOptions as Mock).mockRejectedValueOnce(new Error('server went away'));

    renderPage();
    await user.click(await screen.findByRole('button', { name: /^Continue/ }));

    expect(await screen.findByText('server went away')).toBeInTheDocument();
    expect(mockedNavigate).not.toHaveBeenCalledWith('/migrate');
  });

  it('marks a partial plan as partial', async () => {
    (api.getScope as Mock).mockResolvedValueOnce({
      scope: { mode: 'partial', projects: ['payments'] },
      run: {
        resolution: { projects: ['payments', 'checkout'] },
        previously_migrated: [],
        remaining: ['payments', 'checkout'],
      },
      projects: Array.from({ length: 92 }, (_, i) => ({ slug: `p${i}`, name: `P${i}` })),
      cutover_blocked: false,
    });

    renderPage();
    expect(await screen.findByText('Partial migration')).toBeInTheDocument();
    expect(screen.getByText('2 of 92 projects')).toBeInTheDocument();
  });

  it('goes back to the comparison', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Back' }));
    expect(mockedNavigate).toHaveBeenCalledWith('/compare');
  });

  it('reports a failure to load anything at all', async () => {
    (api.getCompare as Mock).mockRejectedValue(new Error('nope'));
    (api.getPlan as Mock).mockRejectedValue(new Error('nope'));

    renderPage();
    expect(
      await screen.findByText('Neither the comparison nor the plan could be loaded'),
    ).toBeInTheDocument();
  });
});
