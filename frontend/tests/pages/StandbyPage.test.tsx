import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import StandbyPage from '../../src/pages/StandbyPage';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getStandby: vi.fn(),
  setScope: vi.fn(() => Promise.resolve({})),
  finalReportURL: '/api/v1/report/final.html',
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

const STANDBY: api.Standby = {
  configured: true,
  gathered: true,
  full_done: true,
  in_flight: true,
  waiting: true,
  waiting_since: '2026-09-22T16:20:00Z',
  full_at: '2026-09-22T16:15:00Z',
  last_synced_at: '2026-09-23T14:02:00Z',
  top_ups: 2,
  runs: [
    {
      kind: 'top_up', number: 2, at: '2026-09-23T14:02:00Z', projects: 92, created: 1, updated: 14, failed: 0,
      changes: {
        groups: [
          {
            label: 'Secrets', created: 1, updated: 14, deleted: 0, migrated: 0, failed: 0,
            items: [
              { action: 'created', item: 'payments/prd · STRIPE_WEBHOOK_V2' },
              { action: 'updated', item: 'payments/prd · STRIPE_KEY' },
            ],
            more: 13,
          },
        ],
      },
    },
    { kind: 'top_up', number: 1, at: '2026-09-23T09:40:00Z', projects: 92, created: 0, updated: 0, failed: 0 },
    { kind: 'full', at: '2026-09-22T16:15:00Z', projects: 92, created: 19318, updated: 0, failed: 0 },
  ],
  tokens_pending: 1,
  open_errors: 0,
  manual: [{ category: 'integrations', title: 'Integrations', count: 2 }],
};

function renderPage() {
  return render(
    <MemoryRouter>
      <StandbyPage />
    </MemoryRouter>,
  );
}

describe('StandbyPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getStandby as Mock).mockResolvedValue(STANDBY);
  });

  it('says how current the target is, what has run, and what is left', async () => {
    renderPage();
    expect(await screen.findByText('Waiting To Cut Over')).toBeInTheDocument();
    expect(screen.getByText('Top-up 2')).toBeInTheDocument();
    expect(screen.getAllByText('Full migration')).toHaveLength(2); // the strip's label and the run
    // A top-up that found nothing still counts as a check.
    expect(screen.getByText(/nothing had changed/)).toBeInTheDocument();
    expect(screen.getByText('1 service token undecided')).toBeInTheDocument();
    expect(screen.getByText('No open errors')).toBeInTheDocument();
    expect(screen.getByText('2 things to do by hand')).toBeInTheDocument();
  });

  it('starts a top-up over the whole source', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: /Top Up Now/ }));
    expect(api.setScope).toHaveBeenCalledWith({ mode: 'full', projects: [] });
    await waitFor(() => expect(mockedNavigate).toHaveBeenCalledWith('/gathering'));
  });

  it('reads the source before cutover when nothing has been gathered', async () => {
    (api.getStandby as Mock).mockResolvedValue({ ...STANDBY, gathered: false });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: /^Cut Over/ }));
    expect(mockedNavigate).toHaveBeenCalledWith('/gathering', { state: { next: '/cutover' } });
  });

  it('asks for the tokens again after a restart', async () => {
    (api.getStandby as Mock).mockResolvedValue({ ...STANDBY, configured: false });
    renderPage();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Enter Tokens' }));
    // Back here once they are saved, not on through Pre-flight and Gather.
    expect(mockedNavigate).toHaveBeenCalledWith('/configure', { state: { returnTo: '/standby' } });
  });

  it('opens a run to show what it changed', async () => {
    const user = userEvent.setup();
    renderPage();
    const row = await screen.findByRole('button', { name: /Top-up 2/ });
    expect(row).toHaveAttribute('aria-expanded', 'false');

    await user.click(row);
    expect(row).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('1 created · 14 updated')).toBeInTheDocument();
    expect(screen.getByText('payments/prd · STRIPE_KEY')).toBeInTheDocument();
    expect(screen.getByText('and 13 more')).toBeInTheDocument();
  });

  it('says when a run predates the record of changes', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: /Full migration/ }));
    expect(screen.getByText(/Details were not recorded for this run/)).toBeInTheDocument();
  });
});
