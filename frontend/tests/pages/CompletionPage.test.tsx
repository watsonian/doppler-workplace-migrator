import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import CompletionPage from '../../src/pages/CompletionPage';
import * as api from '../../src/api/client';
import type { ErrorsResponse } from '../../src/types/errors';

vi.mock('../../src/api/client', () => ({
  getSummary: vi.fn(),
  getErrors: vi.fn(),
  setScope: vi.fn(() => Promise.resolve({})),
  setStandby: vi.fn(() => Promise.resolve({})),
  errorsExportURL: '/api/v1/errors/export.csv',
  finalReportURL: '/api/v1/report/final.html',
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <CompletionPage />
    </MemoryRouter>,
  );
}

const summaryResponse: api.SummaryResponse = {
  summary: {
    project_count: 92,
    environment_count: 276,
    config_count: 640,
    secret_count: 18412,
    role_count: 6,
    group_count: 37,
    webhook_count: 23,
    service_account_count: 14,
    trusted_ip_count: 6,
    change_request_policy_count: 0,
    manual_step_count: 5,
  },
  checkpoint: {
    started_at: '2026-09-17T09:38:14Z',
    updated_at: '2026-09-17T10:31:20Z',
    phases: {},
    completed: true,
  },
  manual_step_count: 5,
  token_counts: { total: 4, issued: 1, to_recreate: 0, not_needed: 0, pending: 3 },
  manual_steps: [
    { category: 'integration', count: 2 },
    { category: 'sync', count: 2 },
    { category: 'rotated_secret', count: 1 },
  ],
};

const NO_ERRORS: ErrorsResponse = {
  classes: [],
  total: 0,
  open: 0,
  class_count: 0,
  fixable_open: 0,
};

function errorClass(overrides: Partial<ErrorsResponse['classes'][0]> = {}) {
  return {
    key: 'restricted_read_denied',
    title: 'Restricted read denied',
    cause: 'Personal tokens cannot read restricted secrets.',
    severity: 'error' as const,
    remedy: { kind: 'token' as const, label: 'Supply Token And Re-run', detail: '' },
    phase: 5,
    phase_name: 'secrets_and_locks',
    resource: 'secrets',
    resource_label: 'Secrets',
    count: 214,
    resolved_count: 214,
    open_count: 0,
    weight: 6412,
    first_seen: '2026-09-17T09:41:44Z',
    last_seen: '2026-09-17T09:52:00Z',
    sample: { side: 'source' as const, status: 403, attempts: 4, retry_after: 0, latency_ms: 61 },
    resolution: 'Supplied dp.sa.…7f2c · 214 items re-run',
    resolved_at: '2026-09-17T09:47:12Z',
    ...overrides,
  };
}

describe('CompletionPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getSummary as Mock).mockResolvedValue(summaryResponse);
    (api.getErrors as Mock).mockResolvedValue(NO_ERRORS);
  });

  it('shows a loading state while it fetches', () => {
    (api.getSummary as Mock).mockImplementation(() => new Promise(() => {}));
    renderPage();
    expect(screen.getByText(/Loading migration summary/)).toBeInTheDocument();
  });

  it('reports a clean run as complete', async () => {
    renderPage();
    expect(await screen.findByText('Migration Complete')).toBeInTheDocument();
    expect(screen.getByText(/Nothing failed during the run/)).toBeInTheDocument();
  });

  it('shows the counts that were migrated', async () => {
    renderPage();
    await screen.findByText('Migration Complete');
    expect(screen.getByText('92')).toBeInTheDocument();
    expect(screen.getByText('640')).toBeInTheDocument();
    expect(screen.getByText('18,412')).toBeInTheDocument();
  });

  it('calls a run with open errors unfinished, not successful', async () => {
    // A run that reached the end and left 38 failures is not a success with a
    // footnote. The headline has to say so, or nobody acts on it.
    (api.getErrors as Mock).mockResolvedValue({
      classes: [errorClass({ open_count: 38, resolved_count: 0, count: 38, resolution: undefined })],
      total: 38,
      open: 38,
      class_count: 1,
      fixable_open: 38,
    });

    renderPage();
    expect(await screen.findByText('Migration Finished With Work Outstanding')).toBeInTheDocument();
    expect(screen.getByText(/38 errors are still open/)).toBeInTheDocument();
  });

  it('records how a resolved class was cleared', async () => {
    // "Resolved" on its own tells whoever reads this in three months nothing.
    (api.getErrors as Mock).mockResolvedValue({
      classes: [errorClass()],
      total: 214,
      open: 0,
      class_count: 1,
      fixable_open: 0,
    });

    renderPage();
    await screen.findByText('Migration Complete');
    expect(screen.getByText(/Supplied dp\.sa\.…7f2c/)).toBeInTheDocument();
  });

  describe('what is still to do by hand', () => {
    it('names each task, where it stands, and what is in it', async () => {
      // It used to be one total over two sentences — how much was left, but
      // not what, or what doing it meant.
      renderPage();
      await screen.findByText('Still To Do By Hand');

      expect(screen.getByText(/finished on the target by hand/)).toBeInTheDocument();
      expect(screen.getByText('Service Tokens')).toBeInTheDocument();
      expect(screen.getByText('3 of 4 undecided')).toBeInTheDocument();
      expect(screen.getByText('Recreate On The Target')).toBeInTheDocument();
      expect(screen.getByText('5 items')).toBeInTheDocument();
      expect(screen.getByText('2 integrations · 2 secrets syncs · 1 rotated secret')).toBeInTheDocument();
    });

    it('gives each task one way in', async () => {
      const user = userEvent.setup();
      renderPage();

      await user.click(await screen.findByRole('button', { name: 'Issue Or Skip' }));
      expect(mockedNavigate).toHaveBeenCalledWith('/service-tokens');

      await user.click(screen.getByRole('button', { name: 'Open Checklist' }));
      expect(mockedNavigate).toHaveBeenCalledWith('/manual-steps');
    });

    it('keeps the token task when tokens are all that is left', async () => {
      // Tokens are not counted among the manual steps. Without their own count
      // the block would vanish, and the only route to the token page with it.
      (api.getSummary as Mock).mockResolvedValue({ ...summaryResponse, manual_step_count: 0, manual_steps: [] });
      renderPage();

      expect(await screen.findByText('3 of 4 undecided')).toBeInTheDocument();
      expect(screen.queryByText('Recreate On The Target')).not.toBeInTheDocument();
    });

    it('marks the token task done once every token is decided', async () => {
      (api.getSummary as Mock).mockResolvedValue({
        ...summaryResponse,
        manual_step_count: 0,
        manual_steps: [],
        token_counts: { total: 4, issued: 2, to_recreate: 2, not_needed: 0, pending: 0 },
      });
      renderPage();

      expect(await screen.findByText('All 4 handled')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument();
    });
  });

  describe('the actions', () => {
    it('keeps the record on the left, in gray', async () => {
      renderPage();
      const record = await screen.findByRole('button', { name: /Export Record/ });
      expect(record.className).toContain('bg-gray-0');
      expect(record.className).not.toContain('bg-purple-500');
    });

    it('fills only the next step after a full run: cutover', async () => {
      (api.getSummary as Mock).mockResolvedValue({ ...summaryResponse, cutover_blocked: false });
      renderPage();
      await screen.findByRole('button', { name: /Cut Over/ });

      const filled = screen
        .getAllByRole('button')
        .filter((b) => /bg-(purple|red)-500/.test(b.className) && !(b as HTMLButtonElement).disabled);
      expect(filled.map((b) => b.textContent?.trim())).toEqual(['Cut Over']);
    });

    it('sizes a withheld cutover like the buttons beside it', async () => {
      (api.getSummary as Mock).mockResolvedValue({
        ...summaryResponse,
        run: {
          mode: 'partial',
          resolution: { mode: 'partial', projects: ['api'], total_projects: 3, deferred_projects: 2 },
          remaining: 1,
          stage: 'done',
        },
        cutover_blocked: true,
      });
      renderPage();

      const cutover = await screen.findByRole('button', { name: /Cut Over/ });
      const full = screen.getByRole('button', { name: /Run Full Migration/ });
      expect(cutover).toBeDisabled();
      expect(cutover.className).toContain('h-10');
      expect(full.className).toContain('h-10');
    });
  });

  it('offers triage when something is still open', async () => {
    (api.getErrors as Mock).mockResolvedValue({
      classes: [errorClass({ open_count: 38, resolved_count: 0, count: 38, resolution: undefined })],
      total: 38,
      open: 38,
      class_count: 1,
      fixable_open: 38,
    });

    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Open Triage/ }));
    expect(mockedNavigate).toHaveBeenCalledWith('/migrate/errors');
  });

  it('exports the record', async () => {
    const user = userEvent.setup();
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => undefined);
    global.URL.createObjectURL = vi.fn(() => 'blob:record');
    global.URL.revokeObjectURL = vi.fn();

    renderPage();
    await user.click(await screen.findByRole('button', { name: /Export Record/ }));

    expect(clickSpy).toHaveBeenCalled();
    clickSpy.mockRestore();
  });

  it('survives the error list being unavailable', async () => {
    // Triage is supplementary. Losing it must not take down the summary, which
    // is the thing someone came here to read.
    (api.getErrors as Mock).mockRejectedValue(new Error('no errors recorded'));
    renderPage();
    expect(await screen.findByText('Migration Complete')).toBeInTheDocument();
  });

  it('reports a summary that would not load', async () => {
    (api.getSummary as Mock).mockRejectedValue(new Error('Network error'));
    renderPage();
    expect(await screen.findByText('Network error')).toBeInTheDocument();
  });

  it('retries a failed load', async () => {
    const user = userEvent.setup();
    (api.getSummary as Mock)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValue(summaryResponse);

    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Try Again' }));
    await waitFor(() => expect(screen.getByText('Migration Complete')).toBeInTheDocument());
  });
  describe('after a partial run', () => {
    const partialRun = {
      mode: 'partial' as const,
      resolution: {
        mode: 'partial' as const,
        projects: ['payments', 'checkout', 'notifications'],
        chosen: ['payments', 'checkout', 'notifications'],
        deferred_projects: 89,
        deferred_groups: 34,
        deferred_roles: 9,
        deferred_service_accounts: 13,
        deferred_policies: 2,
        defers_workplace_settings: true,
        total_projects: 92,
        configs: 41,
        secrets: 1284,
      },
      remaining: 3,
      stage: 'done' as const,
    };

    beforeEach(() => {
      (api.getSummary as Mock).mockResolvedValue({
        ...summaryResponse,
        summary: { ...summaryResponse.summary, project_count: 3, config_count: 41, secret_count: 1284 },
        source_summary: summaryResponse.summary,
        run: partialRun,
        cutover_blocked: true,
      });
    });

    it('calls itself partial and says how much is left', async () => {
      renderPage();
      expect(await screen.findByText('Partial Migration Complete')).toBeInTheDocument();
      expect(screen.getByText(/3 projects are on the target. The other 89 are still only on the source/)).toBeInTheDocument();
      expect(screen.getByText('of 92')).toBeInTheDocument();
      expect(screen.getByText('Not Migrated Yet')).toBeInTheDocument();
      expect(screen.getByText('Workplace settings and 2 change-request policies')).toBeInTheDocument();
    });

    it('leads with the full run, preselected', async () => {
      const user = userEvent.setup();
      renderPage();
      await user.click(await screen.findByRole('button', { name: /Run Full Migration/ }));

      expect(api.setScope).toHaveBeenCalledWith({ mode: 'full' });
      await waitFor(() => expect(mockedNavigate).toHaveBeenCalledWith('/scope'));
    });

    it('withholds cutover, and says why', async () => {
      // Cutover mirrors trusted IPs and applies deletions — against a target
      // that is missing most of the source.
      renderPage();
      expect(await screen.findByRole('button', { name: /Cut Over/ })).toBeDisabled();
      expect(screen.getByText('Cutover becomes available after a full migration.')).toBeInTheDocument();
    });
  });

  it('records what a full run did to an earlier partial', async () => {
    (api.getSummary as Mock).mockResolvedValue({
      ...summaryResponse,
      cutover_blocked: false,
      run: {
        mode: 'full',
        resolution: { mode: 'full', projects: [], total_projects: 92 },
        remaining: 89,
        previously_migrated: ['payments', 'checkout', 'notifications'],
        previously_migrated_at: '2026-09-21T09:00:00Z',
        stage: 'done',
        top_up: { created: 2, updated: 12, deleted: 0, skipped: 0, failed: 0, secrets_written: 11 },
      },
    });

    renderPage();
    expect(await screen.findByText('This Run')).toBeInTheDocument();
    expect(screen.getByText(/Migrated 89 projects and the rest of the workplace layer/)).toBeInTheDocument();
    expect(screen.getByText(/Brought the 3 projects from the .* partial up to date/)).toBeInTheDocument();
    expect(screen.getByText('14 changes')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Cut Over/ })).toBeEnabled();
  });

  it('says so when the earlier projects could not be brought up to date', async () => {
    (api.getSummary as Mock).mockResolvedValue({
      ...summaryResponse,
      run: {
        mode: 'full',
        resolution: { mode: 'full', projects: [], total_projects: 92 },
        remaining: 89,
        previously_migrated: ['payments'],
        stage: 'done',
        top_up_error: 'reading the target: 503',
      },
    });

    renderPage();
    expect(await screen.findByText(/Could not bring the 1 earlier project up to date/)).toBeInTheDocument();
    expect(screen.getByText(/Compare again to retry/)).toBeInTheDocument();
  });

  describe('what next', () => {
    const standby = (over: Partial<api.Standby> = {}): api.Standby => ({
      configured: true,
      gathered: true,
      full_done: true,
      waiting: false,
      top_ups: 0,
      runs: [{ kind: 'full', at: '2026-09-22T16:15:00Z', projects: 92, created: 19318, updated: 0, failed: 0 }],
      last_run: { kind: 'full', at: '2026-09-22T16:15:00Z', projects: 92, created: 19318, updated: 0, failed: 0 },
      tokens_pending: 0,
      open_errors: 0,
      manual: [],
      ...over,
    });

    it('asks after a full run, with waiting chosen, and records the choice', async () => {
      (api.getSummary as Mock).mockResolvedValue({ ...summaryResponse, standby: standby() });
      const user = userEvent.setup();
      renderPage();

      const wait = await screen.findByRole('radio', { name: /Wait, And Top Up Until Then/ });
      expect(wait).toBeChecked();
      expect(screen.getByRole('radio', { name: /Cut Over Now/ })).not.toBeChecked();

      await user.click(screen.getByRole('button', { name: /^Continue/ }));
      expect(api.setStandby).toHaveBeenCalledWith('wait');
      await waitFor(() => expect(mockedNavigate).toHaveBeenCalledWith('/standby'));
    });

    it('goes to cutover when that is chosen', async () => {
      (api.getSummary as Mock).mockResolvedValue({ ...summaryResponse, standby: standby() });
      const user = userEvent.setup();
      renderPage();

      await user.click(await screen.findByRole('radio', { name: /Cut Over Now/ }));
      await user.click(screen.getByRole('button', { name: /^Continue/ }));
      expect(api.setStandby).toHaveBeenCalledWith('cut_over');
      await waitFor(() => expect(mockedNavigate).toHaveBeenCalledWith('/cutover'));
    });

    it('reads a top-up as a top-up, and offers to keep waiting', async () => {
      const topUp = { kind: 'top_up' as const, number: 3, at: '2026-09-23T16:34:00Z', projects: 92, created: 3, updated: 14, failed: 0 };
      (api.getSummary as Mock).mockResolvedValue({
        ...summaryResponse,
        standby: standby({ waiting: true, top_ups: 3, last_run: topUp, runs: [topUp, ...standby().runs] }),
      });
      renderPage();

      expect(await screen.findByText('Top-Up Complete')).toBeInTheDocument();
      expect(screen.getByText('Top-up 3')).toBeInTheDocument();
      expect(screen.getByRole('radio', { name: /Keep Waiting/ })).toBeChecked();
    });

    it('offers nothing to choose once cut over', async () => {
      (api.getSummary as Mock).mockResolvedValue({
        ...summaryResponse,
        standby: standby({ cutover_at: '2026-09-24T10:00:00Z' }),
      });
      renderPage();

      expect(await screen.findByRole('button', { name: /Download Report/ })).toBeInTheDocument();
      expect(screen.queryByRole('radio', { name: /Cut Over Now/ })).not.toBeInTheDocument();
    });
  });
});
