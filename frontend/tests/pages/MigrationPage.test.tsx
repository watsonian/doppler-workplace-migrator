import { render, screen, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { WizardProvider } from '@/hooks/useWizard';
import { vi, type Mock } from 'vitest';
import MigrationPage from '../../src/pages/MigrationPage';
import * as api from '../../src/api/client';
import type { ErrorsResponse } from '../../src/types/errors';

vi.mock('../../src/api/client', () => ({
  startMigration: vi.fn(),
  pauseMigration: vi.fn(),
  resumeMigration: vi.fn(),
  getMigrationStatus: vi.fn(),
  getErrors: vi.fn(),
  getErrorClass: vi.fn(),
  resolveErrorClass: vi.fn(),
  subscribeSSE: vi.fn(() => ({ unsubscribe: vi.fn(), connected: Promise.resolve() })),
  // A plain first run by default: no partial behind it, so no band and one stage.
  getScope: vi.fn(() => new Promise(() => {})),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/migrate']}>
      <WizardProvider>
        <MigrationPage />
      </WizardProvider>
    </MemoryRouter>,
  );
}

const NO_ERRORS: ErrorsResponse = {
  classes: [],
  total: 0,
  open: 0,
  class_count: 0,
  fixable_open: 0,
};

/** A class of failures, as the server groups them. */
function restrictedReadClass(overrides: Partial<ErrorsResponse['classes'][0]> = {}) {
  return {
    key: 'restricted_read_denied',
    title: 'Restricted read denied',
    cause: 'The source token is user-backed. Personal tokens cannot read restricted secrets.',
    severity: 'error' as const,
    remedy: {
      kind: 'token' as const,
      label: 'Supply Token And Re-run',
      detail: 'Supply a source service account token with access to these projects.',
    },
    phase: 5,
    phase_name: 'secrets_and_locks',
    resource: 'secrets',
    resource_label: 'Secrets',
    count: 214,
    resolved_count: 0,
    open_count: 214,
    weight: 6412,
    first_seen: '2026-09-17T09:41:44Z',
    last_seen: '2026-09-17T09:52:00Z',
    sample: {
      side: 'source' as const,
      token_hint: 'dp.pt.…a91c',
      token_kind: 'personal',
      token_kind_label: 'Personal token',
      actor: 'joel.watson@doppler.com',
      method: 'GET',
      url: 'https://api.doppler.com/v3/configs/config/secrets?project=payments&config=prd',
      status: 403,
      status_text: '403 Forbidden',
      body: '{"messages":["You do not have access"]}',
      attempts: 4,
      retry_after: 0,
      latency_ms: 61,
    },
    projects: ['identity', 'payments'],
    ...overrides,
  };
}

describe('MigrationPage', () => {
  let sseCallback: api.SSEEventHandler;
  let sseUnsubscribe: Mock;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers({ shouldAdvanceTime: true });

    sseUnsubscribe = vi.fn();
    (api.subscribeSSE as Mock).mockImplementation((handler: api.SSEEventHandler) => {
      sseCallback = handler;
      return { unsubscribe: sseUnsubscribe, connected: Promise.resolve() };
    });

    (api.startMigration as Mock).mockResolvedValue({ status: 'started' });
    (api.getMigrationStatus as Mock).mockResolvedValue({ migrating: false });
    (api.getErrors as Mock).mockResolvedValue(NO_ERRORS);
    (api.getErrorClass as Mock).mockResolvedValue({
      class: restrictedReadClass(),
      records: [],
      total: 0,
      truncated: false,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function emit(type: string, data: unknown) {
    act(() => {
      sseCallback(type, data);
    });
  }

  function progress(overrides: Partial<api.MigrateProgress> = {}): api.MigrateProgress {
    return {
      phase: 5,
      name: 'secrets_and_locks',
      resource: 'secrets',
      item: 'web/prd',
      status: 'started',
      current: 217,
      total: 331,
      ...overrides,
    } as api.MigrateProgress;
  }

  it('starts the migration on arrival', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());
    expect(screen.getByText('Migrating')).toBeInTheDocument();
  });

  it('attaches to a run already in progress instead of starting a second one', async () => {
    // A refresh mid-run must not fire a second migration at the same target.
    (api.getMigrationStatus as Mock).mockResolvedValue({ migrating: true, paused: false });
    renderPage();
    await waitFor(() => expect(api.subscribeSSE).toHaveBeenCalled());
    expect(api.startMigration).not.toHaveBeenCalled();
  });

  it('lists every phase on the timeline', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    for (const label of [
      'Workplace & Roles',
      'Groups & Service Accounts',
      'Projects & Environments',
      'Configs',
      'Secrets & Config Locks',
      'Config Inheritance',
      'Webhooks',
      'Members & User Access',
      'Change Request Policies',
      'Trusted IPs',
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });

  it('shows the running phase and what it is writing', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    emit('migrate_progress', progress());

    await waitFor(() => expect(screen.getByText('web/prd')).toBeInTheDocument());
    expect(screen.getByText('217 / 331')).toBeInTheDocument();
  });

  it('nests an error class under the phase that produced it', async () => {
    // The nesting is the point: an error belongs to its phase, and showing the
    // two at the same level makes a ten-phase run read as a flat list.
    (api.getErrors as Mock).mockResolvedValue({
      classes: [restrictedReadClass()],
      total: 214,
      open: 214,
      class_count: 1,
      fixable_open: 214,
    });

    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());
    emit('errors_changed', {});

    const entry = await screen.findByRole('button', { name: /Restricted read denied/ });
    expect(entry).toBeInTheDocument();
    // The count rides on the class rather than expanding into 214 rows.
    expect(entry).toHaveTextContent('214');
  });

  it('shows the cause, the evidence and the fix when a class is selected', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    (api.getErrors as Mock).mockResolvedValue({
      classes: [restrictedReadClass()],
      total: 214,
      open: 214,
      class_count: 1,
      fixable_open: 214,
    });

    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());
    emit('errors_changed', {});

    await user.click(await screen.findByRole('button', { name: /Restricted read denied/ }));

    expect(await screen.findByText(/Personal tokens cannot read restricted secrets/)).toBeInTheDocument();
    // The evidence is what tells you whether the tool is wrong or the setup is.
    expect(screen.getByText('dp.pt.…a91c')).toBeInTheDocument();
    expect(screen.getByText('Personal token')).toBeInTheDocument();
    // Twice, deliberately: the pill is the glanceable state, the evidence row
    // is the record of what actually came back.
    expect(screen.getAllByText('403 Forbidden')).toHaveLength(2);
    expect(screen.getByText(/api\.doppler\.com\/v3\/configs\/config\/secrets/)).toBeInTheDocument();
  });

  it('states the fix scope beside the button rather than inside its label', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    (api.getErrors as Mock).mockResolvedValue({
      classes: [restrictedReadClass()],
      total: 214,
      open: 214,
      class_count: 1,
      fixable_open: 214,
    });

    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());
    emit('errors_changed', {});
    await user.click(await screen.findByRole('button', { name: /Restricted read denied/ }));

    const fixButton = await screen.findByRole('button', { name: /Supply Token And Re-run/ });
    // A label baked with a count is wrong the moment the run records one more.
    expect(fixButton.textContent).not.toMatch(/\d/);
    expect(screen.getByText(/214 secrets · 6,412 secrets/)).toBeInTheDocument();
  });

  it('marks completed phases', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    emit('phase_complete', { phase: 1, name: 'workplace_settings_and_roles', status: 'completed' });

    await waitFor(() => expect(screen.getByText('Complete')).toBeInTheDocument());
  });

  it('waits for the operator rather than advancing itself on completion', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    emit('migration_complete', { status: 'completed' });

    await waitFor(() => expect(screen.getByText('All phases run')).toBeInTheDocument());
    expect(mockedNavigate).not.toHaveBeenCalled();
  });

  it('detects completion by polling when SSE never delivers it', async () => {
    (api.getMigrationStatus as Mock)
      .mockResolvedValueOnce({ migrating: false })
      .mockResolvedValue({ migrating: false, migration_result: 'completed' });

    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3100);
    });

    await waitFor(() => expect(screen.getByText('All phases run')).toBeInTheDocument());
  });

  it('reports a run that stopped', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    emit('migration_complete', { status: 'failed', error: 'context cancelled' });

    await waitFor(() => expect(screen.getByText('context cancelled')).toBeInTheDocument());
    expect(screen.getByText('Stopped')).toBeInTheDocument();
  });

  it('reports a migration that would not start', async () => {
    (api.startMigration as Mock).mockRejectedValue(new Error('Configuration not set'));

    renderPage();
    await waitFor(() => expect(screen.getByText('Configuration not set')).toBeInTheDocument());
  });

  it('pauses and resumes', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    (api.pauseMigration as Mock).mockResolvedValue({ status: 'paused' });
    (api.resumeMigration as Mock).mockResolvedValue({ status: 'resumed' });

    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    await user.click(screen.getByRole('button', { name: /Pause/ }));
    await waitFor(() => expect(api.pauseMigration).toHaveBeenCalled());
    expect(screen.getByText('Paused')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Resume/ }));
    await waitFor(() => expect(api.resumeMigration).toHaveBeenCalled());
  });

  it('only offers Continue once the run has stopped', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    expect(screen.getByRole('button', { name: /^Continue/ })).toBeDisabled();

    emit('migration_complete', { status: 'completed' });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Continue To Summary' })).toBeInTheDocument(),
    );
  });

  it('continues to the summary, not to the token report', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    emit('migration_complete', { status: 'completed' });
    await user.click(await screen.findByRole('button', { name: 'Continue To Summary' }));

    expect(mockedNavigate).toHaveBeenCalledWith('/complete');
  });

  it('offers triage only when there is something to triage', async () => {
    renderPage();
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());
    expect(screen.queryByText('Open Triage')).not.toBeInTheDocument();

    (api.getErrors as Mock).mockResolvedValue({
      classes: [restrictedReadClass()],
      total: 214,
      open: 214,
      class_count: 1,
      fixable_open: 214,
    });
    emit('errors_changed', {});

    await waitFor(() => expect(screen.getByText('Open Triage')).toBeInTheDocument());
  });
  describe('a full run after a partial', () => {
    function afterPartial() {
      (api.getScope as Mock).mockResolvedValue({
        scope: { mode: 'full' },
        run: {
          resolution: { projects: ['payments', 'checkout', 'search', 'billing'] },
          remaining: ['search', 'billing'],
          previously_migrated: ['payments', 'checkout'],
          previously_migrated_at: '2026-09-21T09:00:00Z',
        },
        projects: [],
        cutover_blocked: true,
      });
    }

    it('shows two stages, the second waiting for the first', async () => {
      afterPartial();
      renderPage();

      expect(await screen.findByText('Remaining Resources')).toBeInTheDocument();
      expect(screen.getByText('Previously Migrated')).toBeInTheDocument();
      expect(screen.getByText('Bring 2 earlier projects up to date')).toBeInTheDocument();
      expect(screen.getByText(/After the remaining resources/)).toBeInTheDocument();
    });

    it('follows the second stage as it runs', async () => {
      afterPartial();
      renderPage();
      await screen.findByText('Previously Migrated');
      await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

      emit('migration_stage', { stage: 'previously_migrated' });
      emit('converge_progress', { name: 'converge', item: 'payments/prd · STRIPE_KEY', status: 'completed' });
      emit('converge_progress', { name: 'converge', item: 'checkout/prd', status: 'completed' });

      // "Phase 10 of 10" at this point would read as a hang.
      expect(await screen.findByText('Updating earlier projects')).toBeInTheDocument();
      expect(screen.getByText(/2 changes applied/)).toBeInTheDocument();
    });

    it('carries the band that says what this run is finishing', async () => {
      afterPartial();
      renderPage();
      expect(await screen.findByText('Full migration')).toBeInTheDocument();
      expect(screen.getByText(/completing the partial run/)).toBeInTheDocument();
    });
  });

  describe('a top-up', () => {
    function topUp() {
      (api.getScope as Mock).mockResolvedValue({
        scope: { mode: 'full' },
        run: {
          resolution: { projects: ['api', 'backend', 'something-new', 'web'] },
          remaining: [],
          previously_migrated: ['api', 'backend', 'something-new', 'web'],
        },
        projects: [],
        cutover_blocked: false,
        top_up: { number: 3, since: '2026-09-23T14:02:00Z' },
      });
    }

    it('names its first stage for what it covers', async () => {
      topUp();
      renderPage();
      expect(await screen.findByText('Not Yet On The Target')).toBeInTheDocument();
      expect(screen.queryByText('Remaining Resources')).not.toBeInTheDocument();
    });

    it('migrates a project deleted from the target again, and says so', async () => {
      // The report: the project came back through the top-up, which created it
      // and its configs and nothing in them. It now goes through the first
      // stage, and the second brings only the other three up to date.
      topUp();
      renderPage();
      await screen.findByText('Not Yet On The Target');
      await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

      emit('migration_stage', { stage: 'remaining', previously_migrated: 3, reintroduced: ['something-new'] });

      expect(await screen.findByText('Bring 3 earlier projects up to date')).toBeInTheDocument();
      expect(screen.getByText('something-new')).toBeInTheDocument();
      expect(screen.getByText(/no longer on the target, so it is migrated again in full/)).toBeInTheDocument();
    });
  });

  it('keeps a first run to one stage, with no band', async () => {
    (api.getScope as Mock).mockResolvedValue({
      scope: { mode: 'full' },
      run: { resolution: { projects: ['payments'] }, remaining: ['payments'], previously_migrated: [] },
      projects: [],
      cutover_blocked: false,
    });
    renderPage();
    await waitFor(() => expect(api.getScope).toHaveBeenCalled());
    await waitFor(() => expect(api.startMigration).toHaveBeenCalled());

    expect(screen.queryByText('Remaining Resources')).not.toBeInTheDocument();
    expect(screen.queryByText('Previously Migrated')).not.toBeInTheDocument();
    expect(screen.queryByText('Full migration')).not.toBeInTheDocument();
  });
});
