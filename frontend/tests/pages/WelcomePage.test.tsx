import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import WelcomePage from '../../src/pages/WelcomePage';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getCheckpoint: vi.fn(),
  getStandby: vi.fn(),
}));

/** A migration two partial runs into its target, as the history on disk has it. */
function partialStandby(configured: boolean) {
  return {
    configured,
    gathered: false,
    source_host: 'https://api.doppler.com',
    target_host: 'https://doppler.internal.acme.com',
    full_done: false,
    in_flight: false,
    waiting: false,
    unfinished: true,
    projects_migrated: 3,
    last_synced_at: '2026-09-24T14:02:00Z',
    top_ups: 0,
    runs: [
      { kind: 'partial', at: '2026-09-24T14:02:00Z', projects: 2, created: 40, updated: 0, failed: 0 },
      { kind: 'partial', at: '2026-09-23T10:00:00Z', projects: 1, created: 12, updated: 0, failed: 0 },
    ],
    tokens_pending: 0,
    open_errors: 0,
    manual: [],
  };
}

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <WelcomePage />
    </MemoryRouter>,
  );
}

describe('WelcomePage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getCheckpoint as Mock).mockRejectedValue(new Error('no checkpoint'));
    (api.getStandby as Mock).mockResolvedValue({ ...partialStandby(false), unfinished: false, runs: [] });
  });

  it('offers to carry on a partial migration instead of starting again', async () => {
    const user = userEvent.setup();
    (api.getStandby as Mock).mockResolvedValue(partialStandby(false));

    renderPage();
    expect(await screen.findByText(/is in progress: 3 projects migrated in 2 runs/)).toBeInTheDocument();
    expect(screen.getByText('https://doppler.internal.acme.com')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Get Started/ })).not.toBeInTheDocument();

    // After a restart the tokens are gone, so carrying on starts at Configure.
    const buttons = screen.getAllByRole('button', { name: /Continue Migration/ });
    expect(buttons).toHaveLength(2);
    await user.click(buttons[0]);
    expect(mockedNavigate).toHaveBeenCalledWith('/configure');
  });

  it('carries on at Scope while the tokens are still held', async () => {
    const user = userEvent.setup();
    (api.getStandby as Mock).mockResolvedValue(partialStandby(true));

    renderPage();
    await user.click((await screen.findAllByRole('button', { name: /Continue Migration/ }))[1]);
    expect(mockedNavigate).toHaveBeenCalledWith('/scope');
  });

  it('offers to resume a stopped run ahead of carrying on', async () => {
    (api.getStandby as Mock).mockResolvedValue(partialStandby(true));
    (api.getCheckpoint as Mock).mockResolvedValue({
      started_at: '2026-09-17T09:38:14Z',
      updated_at: '2026-09-17T09:52:06Z',
      completed: false,
      phases: {},
    });

    renderPage();
    expect(await screen.findByRole('button', { name: 'Resume Run' })).toBeInTheDocument();
    expect(screen.queryByText(/is in progress/)).not.toBeInTheDocument();
  });

  it('names what the tool does and what it leaves alone', async () => {
    renderPage();
    expect(screen.getByText('Migrate A Doppler Workplace')).toBeInTheDocument();
    expect(screen.getByText(/Nothing on the source is changed/)).toBeInTheDocument();
  });

  it('lists what is migrated, what is manual, and what is not migrated at all', () => {
    // The useful question before a migration is not "does it work" but "what
    // will I still have to do afterwards", and that is the second and third
    // lists rather than the first.
    renderPage();
    expect(screen.getByText('Migrated Automatically')).toBeInTheDocument();
    expect(screen.getByText('You Will Do By Hand')).toBeInTheDocument();
    expect(screen.getByText('Not Migrated')).toBeInTheDocument();
  });

  it('says that secret visibility travels with the secret', () => {
    renderPage();
    expect(
      screen.getByText('Secrets, with their visibility, value types and notes'),
    ).toBeInTheDocument();
  });

  it('says the workplace name is opt-in', () => {
    renderPage();
    expect(screen.getByText(/The workplace name — off unless you enable it/)).toBeInTheDocument();
  });

  it('warns that users must exist on the target first', () => {
    renderPage();
    expect(screen.getByRole('link', { name: 'Set up SCIM' })).toBeInTheDocument();
  });

  it('starts the wizard at Configure', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole('button', { name: /Get Started/ }));
    expect(mockedNavigate).toHaveBeenCalledWith('/configure');
  });

  it('offers to resume a run that stopped', async () => {
    // Otherwise someone starts again and wonders why the target already has
    // half a workplace in it.
    (api.getCheckpoint as Mock).mockResolvedValue({
      started_at: '2026-09-17T09:38:14Z',
      updated_at: '2026-09-17T09:52:06Z',
      completed: false,
      phases: {
        configs: { name: 'configs', status: 'completed', items: {} },
        secrets_and_locks: { name: 'secrets_and_locks', status: 'in_progress', items: {} },
      },
    });

    renderPage();
    expect(await screen.findByText(/stopped/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Resume Run' })).toBeInTheDocument();
  });

  it('resumes into the migration view', async () => {
    const user = userEvent.setup();
    (api.getCheckpoint as Mock).mockResolvedValue({
      started_at: '2026-09-17T09:38:14Z',
      updated_at: '2026-09-17T09:52:06Z',
      completed: false,
      phases: {},
    });

    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Resume Run' }));
    expect(mockedNavigate).toHaveBeenCalledWith('/migrate');
  });

  it('dismisses the resume offer without discarding anything on the target', async () => {
    const user = userEvent.setup();
    (api.getCheckpoint as Mock).mockResolvedValue({
      started_at: '2026-09-17T09:38:14Z',
      updated_at: '2026-09-17T09:52:06Z',
      completed: false,
      phases: {},
    });

    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Start Fresh' }));
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Resume Run' })).not.toBeInTheDocument(),
    );
  });

  it('does not offer to resume a run that finished', async () => {
    (api.getCheckpoint as Mock).mockResolvedValue({
      started_at: '2026-09-17T09:38:14Z',
      updated_at: '2026-09-17T10:31:20Z',
      completed: true,
      phases: {},
    });

    renderPage();
    await waitFor(() => expect(api.getCheckpoint).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: 'Resume Run' })).not.toBeInTheDocument();
  });

  it('puts what to prepare first, with a way to the source token permissions', async () => {
    const user = userEvent.setup();
    renderPage();

    expect(screen.getByText('Before You Start')).toBeInTheDocument();
    expect(screen.getByText('A service account on the source')).toBeInTheDocument();
    expect(screen.getByText('Users on the target')).toBeInTheDocument();
    const scim = screen.getByRole('link', { name: 'Set up SCIM' });
    expect(scim).toHaveAttribute('href', 'https://docs.doppler.com/docs/scim');
    expect(scim).toHaveAttribute('target', '_blank');

    await user.click(screen.getByRole('button', { name: /Source Token Permissions/ }));
    expect(mockedNavigate).toHaveBeenCalledWith('/permissions');
  });
});

