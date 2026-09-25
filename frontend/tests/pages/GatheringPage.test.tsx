import { render, screen, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import GatheringPage from '../../src/pages/GatheringPage';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  startGather: vi.fn(),
  getGatherStatus: vi.fn(),
  subscribeSSE: vi.fn(() => ({ unsubscribe: vi.fn(), connected: Promise.resolve() })),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <GatheringPage />
    </MemoryRouter>,
  );
}

const mockSummary: api.PlanSummary = {
  project_count: 3,
  environment_count: 9,
  config_count: 15,
  secret_count: 150,
  role_count: 3,
  group_count: 2,
  service_account_count: 1,
  webhook_count: 3,
  trusted_ip_count: 2,
  manual_step_count: 0,
};

describe('GatheringPage', () => {
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

    // The gather runs in the background: POST only acknowledges the start, and the
    // outcome arrives over SSE (with polling as the fallback).
    (api.startGather as Mock).mockResolvedValue({ status: 'started' });
    (api.getGatherStatus as Mock).mockResolvedValue({ gathering: true });
  });

  /** Delivers the completion the server would publish over SSE. */
  async function completeGather(summary = mockSummary) {
    await act(async () => {
      sseCallback('gather_complete', { status: 'complete', summary });
    });
  }

  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders page header and auto-starts gathering', async () => {
    renderPage();

    expect(screen.getByText('Gathering Data')).toBeInTheDocument();
    expect(api.subscribeSSE).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(api.startGather).toHaveBeenCalledTimes(1);
    });
  });

  it('shows overall progress during gathering', async () => {
    // Use a never-resolving promise to keep gathering in progress
    (api.startGather as Mock).mockReturnValue(new Promise(() => {}));

    renderPage();

    await waitFor(() => {
      expect(screen.getByRole('progressbar', { name: 'Gather progress' })).toBeInTheDocument();
    });
  });

  it('updates resource progress from SSE events', async () => {
    // Keep gathering in progress so SSE events are visible
    (api.startGather as Mock).mockReturnValue(new Promise(() => {}));

    renderPage();

    // Simulate SSE events - wrap in act since it triggers state updates
    act(() => {
      sseCallback('gather_progress', {
        resource: 'workplace',
        current: 0,
        total: 1,
        message: 'Workplace info gathered',
      });
    });

    await waitFor(() => {
      expect(screen.getByText('Workplace info gathered')).toBeInTheDocument();
    });
  });

  it('shows progress for multiple resources', async () => {
    // Keep gathering in progress so SSE events are visible
    (api.startGather as Mock).mockReturnValue(new Promise(() => {}));

    renderPage();

    act(() => {
      sseCallback('gather_progress', {
        resource: 'workplace',
        current: 0,
        total: 1,
        message: 'Workplace info gathered',
      });
      sseCallback('gather_progress', {
        resource: 'users',
        current: 4,
        total: 10,
        message: '10 users found',
      });
      sseCallback('gather_progress', {
        resource: 'projects',
        current: 1,
        total: 3,
        message: 'Project "Backend" gathered',
      });
    });

    await waitFor(() => {
      expect(screen.getByText('Workplace info gathered')).toBeInTheDocument();
      expect(screen.getByText('10 users found')).toBeInTheDocument();
      expect(screen.getByText('Project "Backend" gathered')).toBeInTheDocument();
    });
  });

  it('shows the completion summary and waits for the operator', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());
    await completeGather();

    await waitFor(() => {
      expect(screen.getByText(/projects,/)).toBeInTheDocument();
    });

    // Summary should include key counts
    expect(screen.getByText(/3 projects/)).toBeInTheDocument();
    expect(screen.getByText(/15 configs/)).toBeInTheDocument();
    expect(screen.getByText(/150 secrets/)).toBeInTheDocument();

    // No auto-advance: the wizard moves on only when the operator says so, so
    // they can read what was gathered.
    await act(async () => {
      vi.advanceTimersByTime(10000);
    });
    expect(mockedNavigate).not.toHaveBeenCalledWith('/plan');
  });

  it('enables Next button when complete', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());
    await completeGather();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Continue/ })).toBeEnabled();
    });
  });

  it('disables Next button while gathering', async () => {
    // Never completes: no SSE completion event is delivered.

    renderPage();

    expect(screen.getByRole('button', { name: /Continue/ })).toBeDisabled();
  });

  it('shows error alert and retry button on failure', async () => {
    (api.startGather as Mock).mockRejectedValue(new Error('Connection refused'));
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Connection refused')).toBeInTheDocument();
      expect(screen.getByText('Connection refused')).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Try Again' })).toBeInTheDocument();
  });

  it('retries gathering when Retry button is clicked', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    (api.startGather as Mock).mockRejectedValueOnce(new Error('Connection refused'));

    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Connection refused')).toBeInTheDocument();
    });

    // Second attempt starts cleanly, then completes over SSE.
    (api.startGather as Mock).mockResolvedValueOnce({ status: 'started' });
    await user.click(screen.getByRole('button', { name: 'Try Again' }));
    await completeGather();

    await waitFor(() => {
      expect(screen.getByText(/projects,/)).toBeInTheDocument();
    });
  });

  it('unsubscribes from SSE on completion', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());
    await completeGather();

    await waitFor(() => {
      expect(screen.getByText(/projects,/)).toBeInTheDocument();
    });
    expect(sseUnsubscribe).toHaveBeenCalled();
  });

  it('Back button navigates to preflight page', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());
    await completeGather();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Back' })).toBeEnabled();
    });

    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(mockedNavigate).toHaveBeenCalledWith('/preflight');
  });

  it('continues to the scope choice, not straight to the comparison', async () => {
    // The comparison is computed for a scope, so the scope has to be chosen
    // first — and it can only be chosen once the projects have been gathered.
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());
    await completeGather();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Continue/ })).toBeEnabled();
    });

    await user.click(screen.getByRole('button', { name: /Continue/ }));
    expect(mockedNavigate).toHaveBeenCalledWith('/scope');
  });

  it('disables Back button while gathering', async () => {
    // Keep gathering in progress
    (api.startGather as Mock).mockReturnValue(new Promise(() => {}));

    renderPage();

    expect(screen.getByRole('button', { name: 'Back' })).toBeDisabled();
  });
});

describe('GatheringPage — async gather', () => {
  let sseCallback: api.SSEEventHandler;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    (api.subscribeSSE as Mock).mockImplementation((handler: api.SSEEventHandler) => {
      sseCallback = handler;
      return { unsubscribe: vi.fn(), connected: Promise.resolve() };
    });
    (api.startGather as Mock).mockResolvedValue({ status: 'started' });
    (api.getGatherStatus as Mock).mockResolvedValue({ gathering: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // The gather now runs in the background, so the page must not treat the POST
  // returning as the gather being finished.
  it('stays in progress after the start call returns', async () => {
    renderPage();

    await waitFor(() => {
      expect(api.startGather).toHaveBeenCalledTimes(1);
    });
    expect(screen.queryByText('Gathering Complete')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Continue/ })).toBeDisabled();
  });

  // A dropped SSE stream must not strand the wizard on a gather that finished.
  it('detects completion by polling when the SSE event is missed', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());

    (api.getGatherStatus as Mock).mockResolvedValue({
      gathering: false,
      status: 'complete',
      summary: mockSummary,
    });

    await act(async () => {
      vi.advanceTimersByTime(3000);
    });

    await waitFor(() => {
      expect(screen.getByText(/projects,/)).toBeInTheDocument();
    });
  });

  it('surfaces a gather that failed in the background', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());

    await act(async () => {
      sseCallback('gather_complete', { status: 'failed', error: 'rate limited' });
    });

    await waitFor(() => {
      expect(screen.getByText('rate limited')).toBeInTheDocument();
    });
    expect(mockedNavigate).not.toHaveBeenCalledWith('/plan');
  });
});

describe('GatheringPage — every resource is accounted for', () => {
  let sseCallback: api.SSEEventHandler;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    (api.subscribeSSE as Mock).mockImplementation((handler: api.SSEEventHandler) => {
      sseCallback = handler;
      return { unsubscribe: vi.fn(), connected: Promise.resolve() };
    });
    (api.startGather as Mock).mockResolvedValue({ status: 'started' });
    (api.getGatherStatus as Mock).mockResolvedValue({ gathering: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // A resource with no row looks like something the tool forgot about. Every type
  // it reads gets a line, including the ones it only reads for the report.
  it('lists every resource type up front, before any progress arrives', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());

    for (const label of [
      'Workplace Settings',
      'Config Inheritance',
      'Change Request Policies',
      'Service Account Identities',
      'Rotated Secrets',
      'Personal Configs',
      'Trusted IPs',
      'Project Members',
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });

  // "None found" is a result. Showing it greyed out is how the operator can tell
  // the tool looked and there was nothing, rather than that it skipped the step.
  it('greys out a resource that came back empty and says nothing is to be migrated', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());

    await act(async () => {
      sseCallback('gather_progress', {
        resource: 'change_request_policies',
        current: 0,
        total: 0,
        message: '0 change request policies found',
      });
    });

    expect(screen.getByText('None found — nothing to migrate')).toBeInTheDocument();
  });

  // Report-only resources are never migrated, so "nothing to migrate" would be
  // misleading for them.
  it('words an empty report-only resource differently', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());

    await act(async () => {
      sseCallback('gather_progress', {
        resource: 'rotated_secrets',
        current: 0,
        total: 0,
        message: '0 rotated secrets found',
      });
    });

    expect(screen.getByText('None found')).toBeInTheDocument();
  });

  it('says what the source token was refused, and links to the permissions it needs', async () => {
    renderPage();
    await waitFor(() => expect(api.startGather).toHaveBeenCalled());
    await act(async () => {
      sseCallback('gather_complete', {
        status: 'complete',
        summary: mockSummary,
        unavailable: [
          {
            resource: 'webhooks',
            label: 'Webhooks',
            consequence: 'Webhooks are not migrated.',
            permission: 'enclave_project_webhooks (Manage Project Webhooks) on each project',
            message: 'Your workplace does not have access to Webhooks.',
            where: 2,
          },
        ],
      });
    });

    expect(await screen.findByText(/refused one kind of/)).toBeInTheDocument();
    expect(screen.getByText('Webhooks are not migrated.', { exact: false })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'See Source Token Permissions' })).toHaveAttribute('href', '/permissions');
    // It does not stop the migration.
    expect(screen.getByRole('button', { name: /Continue/ })).toBeEnabled();
  });
});
