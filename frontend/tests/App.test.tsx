import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { vi } from 'vitest';
import App from '../src/App';

// Mock fetch globally so pages that auto-call APIs don't cause act() warnings.
// Returns plausible data for each endpoint to prevent component errors.
const mockFetch = vi.fn((url: string) => {
  let body: Record<string, unknown> = { checks: [], passed: false };

  if (typeof url === 'string') {
    if (url.includes('/report/service-tokens')) {
      body = { service_tokens: [] };
    } else if (url.includes('/report/summary')) {
      body = {
        summary: {
          project_count: 0, environment_count: 0, config_count: 0,
          secret_count: 0, role_count: 0, group_count: 0,
          webhook_count: 0, service_account_count: 0, trusted_ip_count: 0,
          manual_step_count: 0,
        },
        checkpoint: null,
        manual_step_count: 0,
      };
    } else if (url.includes('/tokens')) {
      // The worklist the service-token page loads, and the detect call it makes
      // straight after. An empty ledger is the simplest honest answer.
      body = {
        records: [],
        counts: { total: 0, issued: 0, to_recreate: 0, not_needed: 0, pending: 0 },
        gate: { counts: { total: 0, issued: 0, to_recreate: 0, not_needed: 0, pending: 0 }, blocked: false },
      };
    } else if (url.includes('/errors')) {
      // `classes` is always an array from the server — ErrorStore.Classes builds
      // it with make(), so an empty run sends [] rather than null. The catch-all
      // body below has no `classes` at all, and CompletionPage reads it
      // unguarded, so without this branch the page throws into an unhandled
      // error that vitest reports as a possible source of false positives.
      body = { classes: [], total: 0, open: 0, class_count: 0, fixable_open: 0 };
    }
  }

  return Promise.resolve({ ok: true, json: () => Promise.resolve(body) });
}) as unknown as typeof fetch;
vi.stubGlobal('fetch', mockFetch);

// Mock EventSource for pages that use SSE (e.g. GatheringPage, MigrationPage).
// Must fire onopen so subscribeSSE's connected promise resolves.
// Implemented as a `function`, not an arrow: subscribeSSE calls `new EventSource(...)`
// and arrow functions are not constructible.
vi.stubGlobal('EventSource', vi.fn(function () {
  const listeners: Record<string, Function[]> = {};
  const es: Record<string, unknown> = {
    addEventListener: vi.fn((event: string, cb: Function) => {
      listeners[event] = listeners[event] || [];
      listeners[event].push(cb);
    }),
    close: vi.fn(),
    onopen: null,
    onerror: null,
  };
  // Fire onopen in next microtick (after subscribeSSE assigns the handler)
  queueMicrotask(() => {
    if (typeof es.onopen === 'function') (es.onopen as () => void)();
  });
  return es;
}));

function renderWithRouter(route = '/') {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <App />
    </MemoryRouter>,
  );
}

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the product header on every page', () => {
    renderWithRouter('/');
    expect(screen.getByText('Workplace Migration')).toBeInTheDocument();
  });

  it('renders the stepper on wizard pages', () => {
    renderWithRouter('/configure');
    expect(screen.getByRole('navigation', { name: 'Wizard progress' })).toBeInTheDocument();
  });

  it('omits the stepper on the welcome screen', () => {
    // Welcome sits before step 1. A stepper there would have to claim the
    // screen is a step of a run that has not started.
    renderWithRouter('/');
    expect(screen.queryByRole('navigation', { name: 'Wizard progress' })).not.toBeInTheDocument();
  });

  it('renders the welcome page at /', async () => {
    renderWithRouter('/');
    // After asking whether a migration is in flight, which would redirect.
    expect(await screen.findByText('Migrated Automatically')).toBeInTheDocument();
  });

  it('renders the configuration page at /configure', () => {
    renderWithRouter('/configure');
    expect(screen.getByText('Configuration')).toBeInTheDocument();
  });

  it('renders the preflight page at /preflight', async () => {
    renderWithRouter('/preflight');
    expect(screen.getByText('Pre-flight Checks')).toBeInTheDocument();
    // Wait for auto-started preflight API call to settle
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalled();
    });
  });

  it('renders the gathering page at /gathering', async () => {
    renderWithRouter('/gathering');
    expect(screen.getByText('Gathering Data')).toBeInTheDocument();
    // Wait for auto-started gather API call to settle
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalled();
    });
  });

  it('renders the plan review page at /plan', async () => {
    renderWithRouter('/plan');
    expect(screen.getByText(/Loading the plan|What This Run Will Change/)).toBeInTheDocument();
    // Wait for auto-started plan API call to settle
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalled();
    });
  });

  it('renders the manual steps page at /manual-steps', async () => {
    renderWithRouter('/manual-steps');
    expect(screen.getByRole('heading', { name: 'Manual Steps', level: 1 })).toBeInTheDocument();
    // Wait for auto-started manual steps API call to settle
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalled();
    });
  });

  it('renders the migration page at /migrate', async () => {
    renderWithRouter('/migrate');
    expect(screen.getByText('Migrating')).toBeInTheDocument();
    // Wait for auto-started migration API call to settle
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalled();
    });
  });

  it('renders the service token worklist at /service-tokens', async () => {
    renderWithRouter('/service-tokens');
    expect(screen.getByText(/Loading service tokens/)).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Service Tokens', level: 1 })).toBeInTheDocument();
    });
  });

  it('renders the completion page at /complete', async () => {
    renderWithRouter('/complete');
    // CompletionPage shows loading state initially, then content after fetch
    expect(screen.getByText(/Loading migration summary/)).toBeInTheDocument();
    // Wait for auto-started API call to settle
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalled();
    });
  });
});
