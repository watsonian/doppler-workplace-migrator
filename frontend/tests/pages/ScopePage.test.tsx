import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import ScopePage from '../../src/pages/ScopePage';
import * as api from '../../src/api/client';
import type { ScopeResolution, ScopeResponse } from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getScope: vi.fn(),
  setScope: vi.fn(),
  previewScope: vi.fn(),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <ScopePage />
    </MemoryRouter>,
  );
}

function resolution(overrides: Partial<ScopeResolution> = {}): ScopeResolution {
  return {
    mode: 'partial',
    projects: ['payments'],
    chosen: ['payments'],
    included: [],
    deferred_projects: 3,
    deferred_groups: 0,
    deferred_roles: 0,
    deferred_service_accounts: 0,
    deferred_policies: 0,
    defers_workplace_settings: true,
    total_projects: 4,
    configs: 1,
    secrets: 40,
    ...overrides,
  };
}

const PROJECTS = [
  { slug: 'billing', name: 'Billing', configs: 2, secrets: 3 },
  { slug: 'checkout', name: 'Checkout', configs: 3, secrets: 12 },
  { slug: 'payments', name: 'Payments', configs: 4, secrets: 40 },
  { slug: 'search', name: 'Search', configs: 1, secrets: 9 },
];

function scopeResponse(overrides: Partial<ScopeResponse> = {}): ScopeResponse {
  return {
    scope: { mode: 'full' },
    run: { resolution: resolution({ mode: 'full', projects: PROJECTS.map((p) => p.slug) }), remaining: [], previously_migrated: [] },
    projects: PROJECTS,
    cutover_blocked: false,
    ...overrides,
  };
}

describe('ScopePage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getScope as Mock).mockResolvedValue(scopeResponse());
    (api.setScope as Mock).mockResolvedValue(scopeResponse());
    (api.previewScope as Mock).mockResolvedValue({ resolution: resolution() });
  });

  it('opens on a full migration', async () => {
    renderPage();
    expect(await screen.findByRole('radio', { name: /Full Migration/ })).toBeChecked();
    expect(screen.queryByPlaceholderText(/Search/)).not.toBeInTheDocument();
    expect(screen.getByText(/4 projects, 10 configs and 64 secrets/)).toBeInTheDocument();
  });

  it('continues with a full scope', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: /Continue/ }));

    expect(api.setScope).toHaveBeenCalledWith({ mode: 'full' });
    await waitFor(() => expect(mockedNavigate).toHaveBeenCalledWith('/compare'));
  });

  it('opens the picker for a partial, and waits for a project', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));

    expect(screen.getByPlaceholderText('Search 4 projects')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Continue/ })).toBeDisabled();
  });

  it('sends the ticked projects', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));
    await user.click(screen.getByRole('checkbox', { name: 'Payments' }));
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    expect(api.setScope).toHaveBeenCalledWith({ mode: 'partial', projects: ['payments'] });
  });

  it('keeps a count out of the button label', async () => {
    // Labels never carry counts: a label with a number is wrong the moment it
    // changes. The count sits in the footer under the list instead.
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));
    await user.click(screen.getByRole('checkbox', { name: 'Payments' }));

    expect(screen.getByRole('button', { name: /Continue/ }).textContent).not.toMatch(/\d/);
    expect(await screen.findByText(/1 of 4 projects/)).toBeInTheDocument();
  });

  it('filters by search without losing the selection', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));
    await user.click(screen.getByRole('checkbox', { name: 'Payments' }));
    await user.type(screen.getByPlaceholderText('Search 4 projects'), 'sea');

    expect(screen.getByRole('checkbox', { name: 'Search' })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Payments' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /Continue/ }));
    expect(api.setScope).toHaveBeenCalledWith({ mode: 'partial', projects: ['payments'] });
  });

  it('shows what the selection brings along, and why', async () => {
    const user = userEvent.setup();
    (api.previewScope as Mock).mockResolvedValue({
      resolution: resolution({
        included: [
          { kind: 'group', key: 'payments-eng', name: 'payments-eng', reasons: ['member of payments, with Deployer'] },
          { kind: 'project_role', key: 'deployer', name: 'Deployer', reasons: ['used in payments'] },
        ],
        deferred_policies: 2,
      }),
    });

    renderPage();
    await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));
    await user.click(screen.getByRole('checkbox', { name: 'Payments' }));

    expect(await screen.findByText('payments-eng')).toBeInTheDocument();
    expect(screen.getByText('Deployer')).toBeInTheDocument();
    expect(screen.getByText('Left For The Full Run')).toBeInTheDocument();
    expect(screen.getByText('2 change-request policies')).toBeInTheDocument();
    expect(screen.getByText('Workplace settings')).toBeInTheDocument();
  });

  it('warns when a selected project loses its review requirement', async () => {
    const user = userEvent.setup();
    (api.previewScope as Mock).mockResolvedValue({
      resolution: resolution({ policies_affected: ['Payments review'] }),
    });

    renderPage();
    await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));
    await user.click(screen.getByRole('checkbox', { name: 'Payments' }));

    expect(await screen.findByText(/Change requests will not be required/)).toBeInTheDocument();
  });

  it('says when inheritance added a project', async () => {
    const user = userEvent.setup();
    (api.previewScope as Mock).mockResolvedValue({
      resolution: resolution({
        projects: ['checkout', 'shared'],
        added: [{ project: 'shared', because: 'checkout/prd inherits from shared/base' }],
      }),
    });

    renderPage();
    await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));
    await user.click(screen.getByRole('checkbox', { name: 'Checkout' }));

    expect(await screen.findByText(/shared was added to keep an inheritance intact/)).toBeInTheDocument();
  });

  describe('after an earlier partial', () => {
    beforeEach(() => {
      (api.getScope as Mock).mockResolvedValue(
        scopeResponse({
          projects: PROJECTS.map((p) => (p.slug === 'payments' ? { ...p, migrated_at: '2026-09-21T09:00:00Z' } : p)),
          run: {
            resolution: resolution({ mode: 'full', projects: PROJECTS.map((p) => p.slug) }),
            remaining: ['billing', 'checkout', 'search'],
            previously_migrated: ['payments'],
            previously_migrated_at: '2026-09-21T09:00:00Z',
          },
        }),
      );
    });

    it('describes the full run as finishing the job', async () => {
      renderPage();
      const full = await screen.findByText('Full Migration');
      const card = full.closest('label')!;
      expect(within(card).getByText('Migrate the other 3 projects')).toBeInTheDocument();
      expect(within(card).getByText('Bring the 1 earlier project up to date')).toBeInTheDocument();
    });

    it('keeps the earlier project ticked and locked', async () => {
      // Unticking would suggest the run removes it from the target, and nothing
      // in a migration deletes a project for being left out.
      const user = userEvent.setup();
      renderPage();
      await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));

      const earlier = screen.getByRole('checkbox', { name: 'Payments' });
      expect(earlier).toBeChecked();
      expect(earlier).toBeDisabled();
      expect(screen.getByText(/^Migrated /)).toBeInTheDocument();
    });

    it('needs a new project before a second partial can continue', async () => {
      const user = userEvent.setup();
      renderPage();
      await user.click(await screen.findByRole('radio', { name: /Partial Migration/ }));
      expect(screen.getByRole('button', { name: /Continue/ })).toBeDisabled();

      await user.click(screen.getByRole('checkbox', { name: 'Search' }));
      await user.click(screen.getByRole('button', { name: /Continue/ }));
      // Only the new one is sent; the server carries the earlier one itself.
      expect(api.setScope).toHaveBeenCalledWith({ mode: 'partial', projects: ['search'] });
    });
  });

  it('reports a scope the server refused', async () => {
    const user = userEvent.setup();
    (api.setScope as Mock).mockRejectedValue(new Error('the source has no project called "x"'));
    renderPage();
    await user.click(await screen.findByRole('button', { name: /Continue/ }));

    expect(await screen.findByText(/no project called/)).toBeInTheDocument();
    expect(mockedNavigate).not.toHaveBeenCalled();
  });

  it('reports projects that would not load', async () => {
    (api.getScope as Mock).mockRejectedValue(new Error('Nothing gathered yet.'));
    renderPage();
    expect(await screen.findByText('Nothing gathered yet.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Try Again' })).toBeInTheDocument();
  });
});
