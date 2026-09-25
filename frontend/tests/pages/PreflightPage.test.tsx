import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { WizardProvider } from '@/hooks/useWizard';
import { vi, type Mock } from 'vitest';
import PreflightPage from '../../src/pages/PreflightPage';
import * as api from '../../src/api/client';
import type { PreflightCheck } from '../../src/types/migration';

vi.mock('../../src/api/client', () => ({
  runPreflight: vi.fn(),
  configure: vi.fn(),
  getConfiguration: vi.fn(),
  subscribeSSE: vi.fn(() => ({ unsubscribe: vi.fn(), connected: Promise.resolve() })),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/preflight']}>
      <WizardProvider>
        <PreflightPage />
      </WizardProvider>
    </MemoryRouter>,
  );
}

const allPassed: PreflightCheck[] = [
  { id: 'source_token', name: 'Source token valid', status: 'passed', message: 'Authenticated' },
  { id: 'target_reachable', name: 'Target host reachable', status: 'passed', message: 'OK' },
];

/**
 * The blocking check the whole design exists for: a class of failures predicted
 * before it happens, with the same three parts a run-time error carries.
 */
const restrictedReadBlocked: PreflightCheck = {
  id: 'source_restricted_secrets',
  name: 'Source token can read restricted secrets',
  status: 'failed',
  message: 'Source token cannot read restricted secrets',
  detail:
    'This is a user-backed token. Personal and CLI tokens cannot read secrets marked restricted.',
  scope: {
    affected: 214,
    total: 640,
    unit: 'configs',
    estimated: true,
    description:
      '9 of 25 configs sampled could not be read. At that rate roughly 214 of 640 configs would fail during the run.',
  },
  evidence: {
    side: 'source',
    token_hint: 'dp.pt.…a91c',
    token_kind: 'personal',
    token_kind_label: 'Personal token',
    actor: 'joel.watson@doppler.com',
    method: 'GET',
    url: 'https://api.doppler.com/v3/configs/config/secrets?project=payments&config=prd',
    status: 403,
    status_text: '403 Forbidden',
    body: '{"messages":["You do not have access to one or more secrets in this config"]}',
    attempts: 1,
    retry_after: 0,
    latency_ms: 58,
  },
  fix: {
    kind: 'token',
    label: 'Save And Re-check',
    field: 'source_token',
    hint: 'Create a service account token with read access to the affected projects.',
    blocking: true,
  },
};

const withWarning: PreflightCheck[] = [
  ...allPassed,
  {
    id: 'users_provisioned',
    name: 'Users provisioned on target',
    status: 'warning',
    message: '4 of 1204 users are missing on the target',
    detail: 'Provision them with SCIM, then re-check.',
  },
];

describe('PreflightPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.subscribeSSE as Mock).mockImplementation(() => ({
      unsubscribe: vi.fn(),
      connected: Promise.resolve(),
    }));
    (api.runPreflight as Mock).mockResolvedValue({
      checks: allPassed,
      passed: true,
      has_warnings: false,
      dev_mode: false,
    });
    (api.getConfiguration as Mock).mockResolvedValue({
      configured: true,
      source_host: 'https://api.doppler.com',
      target_host: 'https://doppler.internal.acme.com',
      has_source_token: true,
      has_target_token: true,
      dev_mode: false,
      dev_mode_forced: false,
      migrate_workplace_name: false,
    });
    (api.configure as Mock).mockResolvedValue({ valid: true, dev_mode: false });
  });

  it('runs the checks on arrival and lists the results', async () => {
    renderPage();
    await waitFor(() => expect(api.runPreflight).toHaveBeenCalled());
    expect(await screen.findByText('Source token valid')).toBeInTheDocument();
    expect(screen.getByText('Target host reachable')).toBeInTheDocument();
  });

  it('says plainly when nothing would have failed', async () => {
    renderPage();
    expect(
      await screen.findByText(/Nothing here would have failed during the run/),
    ).toBeInTheDocument();
  });

  it('allows Continue when everything passes', async () => {
    renderPage();
    await waitFor(() => expect(api.runPreflight).toHaveBeenCalled());
    expect(await screen.findByRole('button', { name: /Continue/ })).not.toBeDisabled();
  });

  it('blocks Continue when a check is blocking', async () => {
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [...allPassed, restrictedReadBlocked],
      passed: false,
      has_warnings: false,
      dev_mode: false,
    });

    renderPage();
    await waitFor(() => expect(api.runPreflight).toHaveBeenCalled());
    expect(await screen.findByRole('button', { name: /Continue/ })).toBeDisabled();
  });

  it('does not block Continue on a warning alone', async () => {
    // A warning is something to act on, not a refusal. The operator decides.
    (api.runPreflight as Mock).mockResolvedValue({
      checks: withWarning,
      passed: true,
      has_warnings: true,
      dev_mode: false,
    });

    renderPage();
    await waitFor(() => expect(api.runPreflight).toHaveBeenCalled());
    expect(await screen.findByRole('button', { name: /Continue/ })).not.toBeDisabled();
    expect(screen.getByText('Warning')).toBeInTheDocument();
  });

  it('expands the blocking check without being asked', async () => {
    // It is the thing that has to be dealt with; making someone hunt for it
    // wastes the attention the screen exists to direct.
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [...allPassed, restrictedReadBlocked],
      passed: false,
      has_warnings: false,
      dev_mode: false,
    });

    renderPage();
    expect(
      await screen.findByText(/Personal and CLI tokens cannot read secrets marked restricted/),
    ).toBeInTheDocument();
  });

  it('shows how much a blocking check would cost', async () => {
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [restrictedReadBlocked],
      passed: false,
      has_warnings: false,
      dev_mode: false,
    });

    renderPage();
    expect(await screen.findByText(/roughly 214 of 640 configs would fail/)).toBeInTheDocument();
    // An estimate reported as fact is worse than no number at all.
    expect(screen.getByText(/comes from a sample rather than a full count/)).toBeInTheDocument();
  });

  it('shows the probe that proved it — token, request and response', async () => {
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [restrictedReadBlocked],
      passed: false,
      has_warnings: false,
      dev_mode: false,
    });

    renderPage();
    expect(await screen.findByText('dp.pt.…a91c')).toBeInTheDocument();
    expect(screen.getByText('Personal token')).toBeInTheDocument();
    expect(screen.getByText('joel.watson@doppler.com')).toBeInTheDocument();
    expect(screen.getByText(/api\.doppler\.com\/v3\/configs\/config\/secrets/)).toBeInTheDocument();
    expect(screen.getByText('403 Forbidden')).toBeInTheDocument();
  });

  it('fixes a blocking check in place and re-checks', async () => {
    const user = userEvent.setup();
    (api.runPreflight as Mock)
      .mockResolvedValueOnce({
        checks: [restrictedReadBlocked],
        passed: false,
        has_warnings: false,
        dev_mode: false,
      })
      .mockResolvedValue({ checks: allPassed, passed: true, has_warnings: false, dev_mode: false });

    renderPage();
    const field = await screen.findByLabelText('Replacement token');
    await user.type(field, 'dp.sa.replacement7f2c');
    await user.click(screen.getByRole('button', { name: /Save And Re-check/ }));

    await waitFor(() =>
      expect(api.configure).toHaveBeenCalledWith(
        expect.objectContaining({ source_token: 'dp.sa.replacement7f2c' }),
      ),
    );
    await waitFor(() => expect(api.runPreflight).toHaveBeenCalledTimes(2));
  });

  it('masks the replacement-token field', async () => {
    // A token on screen is a token in a screenshot and in a screen share, and
    // this is the screen most likely to be shared while one is pasted.
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [restrictedReadBlocked],
      passed: false,
      has_warnings: false,
      dev_mode: false,
    });

    renderPage();
    expect(await screen.findByLabelText('Replacement token')).toHaveAttribute('type', 'password');
  });

  it('will not save an empty token', async () => {
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [restrictedReadBlocked],
      passed: false,
      has_warnings: false,
      dev_mode: false,
    });

    renderPage();
    expect(await screen.findByRole('button', { name: /Save And Re-check/ })).toBeDisabled();
  });

  it('reports a rejected replacement token without losing the screen', async () => {
    const user = userEvent.setup();
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [restrictedReadBlocked],
      passed: false,
      has_warnings: false,
      dev_mode: false,
    });
    (api.configure as Mock).mockRejectedValue(new Error('Source token must start with dp.'));

    renderPage();
    await user.type(await screen.findByLabelText('Replacement token'), 'nonsense');
    await user.click(screen.getByRole('button', { name: /Save And Re-check/ }));

    expect(await screen.findByText('Source token must start with dp.')).toBeInTheDocument();
  });

  it('re-checks everything on request', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(api.runPreflight).toHaveBeenCalledTimes(1));

    await user.click(screen.getByRole('button', { name: /Re-check All/ }));
    await waitFor(() => expect(api.runPreflight).toHaveBeenCalledTimes(2));
  });

  it('collapses a check that was expanded', async () => {
    const user = userEvent.setup();
    (api.runPreflight as Mock).mockResolvedValue({
      checks: withWarning,
      passed: true,
      has_warnings: true,
      dev_mode: false,
    });

    renderPage();
    const row = await screen.findByRole('button', { name: /Users provisioned on target/ });
    await user.click(row);
    expect(await screen.findByText(/Provision them with SCIM/)).toBeInTheDocument();

    await user.click(row);
    await waitFor(() =>
      expect(screen.queryByText(/Provision them with SCIM/)).not.toBeInTheDocument(),
    );
  });

  it('marks dev-mode checks as skipped', async () => {
    (api.runPreflight as Mock).mockResolvedValue({
      checks: [
        ...allPassed,
        { id: 'target_write', name: 'Target has write access', status: 'skipped', message: 'Skipped — Dev Mode' },
      ],
      passed: true,
      has_warnings: false,
      dev_mode: true,
    });

    renderPage();
    expect(await screen.findByText('Skipped')).toBeInTheDocument();
  });

  it('reports a failure to run the checks at all', async () => {
    (api.runPreflight as Mock).mockRejectedValue(new Error('Configuration not set'));
    renderPage();
    expect(await screen.findByText('Configuration not set')).toBeInTheDocument();
  });

  it('continues to the gather step', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: /Continue/ }));
    expect(mockedNavigate).toHaveBeenCalledWith('/gathering');
  });
});
