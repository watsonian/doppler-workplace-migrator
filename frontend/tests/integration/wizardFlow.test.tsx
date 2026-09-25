import { render, screen, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, beforeEach, afterEach, describe, it, expect } from 'vitest';
import App from '../../src/App';

// SSE event listeners captured from EventSource mock
let sseListeners: Record<string, ((e: unknown) => void)[]> = {};

/** Fire an SSE event through the mock EventSource */
function fireSseEvent(eventType: string, data: unknown) {
  if (sseListeners[eventType]) {
    sseListeners[eventType].forEach((h) => h({ data: JSON.stringify(data) }));
  }
}

// Build endpoint-specific fetch responses
function buildMockFetch() {
  return vi.fn((url: string, opts?: RequestInit) => {
    // POST /api/v1/configure
    if (url.includes('/configure') && opts?.method === 'POST') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ valid: true, dev_mode: false }),
      });
    }

    // POST /api/v1/preflight — returns full results synchronously
    if (url.includes('/preflight') && opts?.method === 'POST') {
      const checks = [
        { id: 'source_token', name: 'Source Token', status: 'passed', message: 'OK' },
        { id: 'source_workplace', name: 'Source Workplace', status: 'passed', message: 'Connected' },
        { id: 'target_reachable', name: 'Target Reachable', status: 'passed', message: 'OK' },
      ];
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ checks, passed: true, dev_mode: false }),
      });
    }

    // POST /api/v1/gather — returns full results synchronously
    if (url.includes('/gather') && opts?.method === 'POST') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({
          status: 'complete',
          summary: {
            project_count: 2,
            environment_count: 4,
            config_count: 6,
            secret_count: 20,
            role_count: 2,
            group_count: 1,
            webhook_count: 1,
            service_account_count: 1,
            trusted_ip_count: 1,
            manual_step_count: 0,
          },
        }),
      });
    }

    // GET /api/v1/plan
    if (url.includes('/plan')) {
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            summary: {
              project_count: 2,
              environment_count: 4,
              config_count: 6,
              secret_count: 20,
              role_count: 2,
              group_count: 1,
              webhook_count: 1,
              service_account_count: 1,
              trusted_ip_count: 1,
              manual_step_count: 0,
            },
            workplace: {
              settings: true,
              workplace_roles: ['custom-admin'],
              project_roles: ['deployer'],
              groups: [{ name: 'Engineering', member_count: 2 }],
              service_accounts: ['CI Bot'],
            },
            projects: [
              {
                name: 'Backend',
                description: 'API',
                environments: [{ name: 'Development', slug: 'dev' }],
                configs: [{ name: 'dev', environment: 'dev', root: true, secret_count: 10, trusted_ips: 0, locked: false, inheritable: false }],
                webhooks: [],
                members: [],
              },
            ],
            manual_steps: [],
            user_check: { required: [], existing: [], missing: [] },
          }),
      });
    }

    // GET /api/v1/report/manual-steps
    if (url.includes('/report/manual-steps')) {
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            manual_steps: [
              {
                category: 'sync',
                title: 'Secrets Syncs',
                description: 'Reminder to recreate syncs.',
                items: null,
              },
            ],
            user_check: { required: [], existing: [], missing: [] },
          }),
      });
    }

    // POST /api/v1/migrate — returns 202, delivers results via SSE
    if (url.includes('/migrate') && opts?.method === 'POST' && !url.includes('/pause') && !url.includes('/resume')) {
      Promise.resolve().then(() => {
        fireSseEvent('migration_complete', { status: 'completed' });
      });
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ status: 'started' }),
      });
    }

    // GET /api/v1/report/service-tokens
    if (url.includes('/report/service-tokens')) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ service_tokens: [] }),
      });
    }

    // GET /api/v1/report/summary
    if (url.includes('/report/summary')) {
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            summary: {
              project_count: 2,
              environment_count: 4,
              config_count: 6,
              secret_count: 20,
              role_count: 2,
              group_count: 1,
              webhook_count: 1,
              service_account_count: 1,
              trusted_ip_count: 1,
              manual_step_count: 0,
            },
            checkpoint: {
              started_at: '2025-01-01T00:00:00Z',
              updated_at: '2025-01-01T00:05:00Z',
              phases: {},
              completed: true,
            },
            manual_step_count: 0,
          }),
      });
    }

    // GET /api/v1/status
    if (url.includes('/status')) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ migrating: false, paused: false }),
      });
    }

    // Fallback
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve({}),
    });
  }) as unknown as typeof fetch;
}

// Mock EventSource — auto-fires onopen to unblock subscribeSSE
// Implemented as a `function`, not an arrow: subscribeSSE calls `new EventSource(...)`
// and arrow functions are not constructible.
function createMockEventSource() {
  return vi.fn(function () {
    const instance: Record<string, unknown> = {
      addEventListener: vi.fn((event: string, handler: (e: unknown) => void) => {
        if (!sseListeners[event]) sseListeners[event] = [];
        sseListeners[event].push(handler);
      }),
      close: vi.fn(),
      onopen: null,
      onerror: null,
    };
    // Fire onopen in next microtick (after subscribeSSE assigns the handler)
    queueMicrotask(() => {
      if (typeof instance.onopen === 'function') (instance.onopen as () => void)();
    });
    return instance;
  });
}

function renderApp(route = '/') {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <App />
    </MemoryRouter>,
  );
}

describe('Wizard Flow Integration', () => {
  let mockFetch: ReturnType<typeof buildMockFetch>;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockFetch = buildMockFetch();
    vi.stubGlobal('fetch', mockFetch);
    sseListeners = {};
    vi.stubGlobal('EventSource', createMockEventSource());
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('navigates from Welcome through Configure to Preflight', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderApp('/');

    // Welcome page
    expect(await screen.findByText('Migrate A Doppler Workplace')).toBeInTheDocument();

    // Click Get Started -> navigate to Configure
    await user.click(screen.getByRole('button', { name: 'Get Started' }));

    await waitFor(() => {
      expect(screen.getByText('Configuration')).toBeInTheDocument();
    });

    // Source first: the target card stays locked until it is filled in.
    await user.type(screen.getByLabelText('API token'), 'dp.sa.source123');

    const targetToken = (await screen.findAllByLabelText('API token'))[1];
    await user.type(screen.getByLabelText('Host'), 'https://doppler.example.com');
    await user.type(targetToken, 'dp.sa.target456');

    await user.click(screen.getByRole('button', { name: /Continue/ }));

    // Should navigate to preflight after successful config
    await waitFor(() => {
      expect(screen.getByText('Pre-flight Checks')).toBeInTheDocument();
    });

    // Preflight should auto-run and show results via SSE
    await waitFor(() => {
      expect(screen.getByText(/Nothing here would have failed/)).toBeInTheDocument();
    });
  });

  it('shows configuration error and allows retry', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    // First call fails, second succeeds
    let callCount = 0;
    mockFetch = vi.fn((url: string, opts?: RequestInit) => {
      if (url.includes('/configure') && opts?.method === 'POST') {
        callCount++;
        if (callCount === 1) {
          return Promise.resolve({
            ok: true,
            json: () =>
              Promise.resolve({
                valid: false,
                errors: ['Source token is invalid'],
              }),
          });
        }
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ valid: true, dev_mode: false }),
        });
      }
      return buildMockFetch()(url, opts);
    }) as unknown as typeof fetch;
    vi.stubGlobal('fetch', mockFetch);

    renderApp('/configure');

    await user.type(screen.getByLabelText('API token'), 'dp.sa.source123');
    const targetInput = (await screen.findAllByLabelText('API token'))[1];
    const hostInput = screen.getByLabelText('Host');

    await user.type(hostInput, 'https://doppler.example.com');
    await user.type(targetInput, 'dp.pt.target456');

    // First attempt — should show error
    await user.click(screen.getByRole('button', { name: /Continue/ }));
    await waitFor(() => {
      expect(screen.getByText('Source token is invalid')).toBeInTheDocument();
    });

    // Still on configuration page
    expect(screen.getByText('Configuration')).toBeInTheDocument();

    // Second attempt — should succeed and navigate
    await user.click(screen.getByRole('button', { name: /Continue/ }));
    await waitFor(() => {
      expect(screen.getByText('Pre-flight Checks')).toBeInTheDocument();
    });
  });

  it('stepper shows correct step at each page', async () => {
    // Welcome is before step 1, so it has no stepper at all.
    const { unmount } = renderApp('/');
    expect(screen.queryByRole('navigation', { name: 'Wizard progress' })).toBeNull();
    let stepper: HTMLElement;
    let currentStep: Element | null;
    unmount();

    // Configure is step 1.
    const { unmount: unmount2 } = renderApp('/configure');
    stepper = screen.getByRole('navigation', { name: 'Wizard progress' });
    currentStep = stepper.querySelector('[aria-current="step"]');
    expect(currentStep).not.toBeNull();
    expect(currentStep!.getAttribute('aria-label')).toBe('Step 1: Configure');
    unmount2();

    // Pre-flight is step 2.
    const { unmount: unmount3 } = renderApp('/preflight');
    stepper = screen.getByRole('navigation', { name: 'Wizard progress' });
    currentStep = stepper.querySelector('[aria-current="step"]');
    expect(currentStep).not.toBeNull();
    expect(currentStep!.getAttribute('aria-label')).toBe('Step 2: Pre-flight');
    unmount3();
  });

  it('back navigation works across pages', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    // Start at Welcome, navigate forward to Configure
    renderApp('/');

    // Welcome -> Configure
    await user.click(await screen.findByRole('button', { name: 'Get Started' }));
    await waitFor(() => {
      expect(screen.getByText('Configuration')).toBeInTheDocument();
    });

    // Configure -> Welcome (via Back button)
    await user.click(screen.getByRole('button', { name: 'Back' }));
    await waitFor(() => {
      expect(screen.getByText('Migrate A Doppler Workplace')).toBeInTheDocument();
    });
  });
});
