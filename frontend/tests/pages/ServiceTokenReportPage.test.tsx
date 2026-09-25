import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import ServiceTokenReportPage from '../../src/pages/ServiceTokenReportPage';
import * as api from '../../src/api/client';
import type { TokenCounts, TokenRecord } from '../../src/api/client';

vi.mock('../../src/api/client', async () => {
  const actual = await vi.importActual<typeof api>('../../src/api/client');
  return {
    ...actual,
    getTokens: vi.fn(),
    detectExistingTokens: vi.fn(),
    issueToken: vi.fn(),
    rerollToken: vi.fn(),
    skipToken: vi.fn(),
    skipAllTokens: vi.fn(),
    undoTokenSkip: vi.fn(),
    deferToken: vi.fn(),
    getStandby: vi.fn(() => Promise.reject(new Error('none'))),
  };
});

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <ServiceTokenReportPage />
    </MemoryRouter>,
  );
}

function record(overrides: Partial<TokenRecord> = {}): TokenRecord {
  return {
    key: 'st:payments/prd/ci-deploy',
    kind: 'service_token',
    project: 'payments',
    config: 'prd',
    environment: 'production',
    name: 'ci-deploy',
    access: 'read',
    disposition: 'pending',
    ...overrides,
  };
}

function counts(overrides: Partial<TokenCounts> = {}): TokenCounts {
  return { total: 1, issued: 0, to_recreate: 0, not_needed: 0, pending: 1, ...overrides };
}

function tokensResponse(records: TokenRecord[], c: TokenCounts) {
  return { records, counts: c, gate: { counts: c, blocked: c.pending > 0 } };
}

/** The page loads, then immediately asks the target what already exists. */
function stubLoad(records: TokenRecord[], c: TokenCounts) {
  (api.getTokens as Mock).mockResolvedValue(tokensResponse(records, c));
  (api.detectExistingTokens as Mock).mockResolvedValue({
    ...tokensResponse(records, c),
    detected: 0,
  });
}

describe('ServiceTokenReportPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubLoad([record()], counts());
  });

  it('lists a token with its placement and access', async () => {
    renderPage();
    expect(await screen.findByText('ci-deploy')).toBeInTheDocument();
    expect(screen.getByText('payments / prd')).toBeInTheDocument();
    expect(screen.getByText('read')).toBeInTheDocument();
  });

  it('carries on when the target cannot be asked what already exists', async () => {
    // Detection is an optimisation. Failing it must not take the worklist down
    // with it, because the worklist is still correct without it.
    (api.detectExistingTokens as Mock).mockRejectedValue(new Error('403'));

    renderPage();
    expect(await screen.findByText('ci-deploy')).toBeInTheDocument();
    expect(screen.queryByText(/Could not load/)).not.toBeInTheDocument();
  });

  describe('the cutover gate', () => {
    it('holds Continue while anything is undecided', async () => {
      renderPage();
      const cont = await screen.findByRole('button', { name: /Continue To Cutover/ });
      expect(cont).toBeDisabled();
      expect(screen.getByText(/1 still undecided/)).toBeInTheDocument();
    });

    it('counts a skip as handled, so a workplace that issues nothing still gets through', async () => {
      // The whole reason skipping is a decision rather than an omission.
      stubLoad(
        [record({ disposition: 'to_recreate' })],
        counts({ pending: 0, to_recreate: 1 }),
      );

      renderPage();
      const cont = await screen.findByRole('button', { name: /Continue To Cutover/ });
      expect(cont).toBeEnabled();
      expect(screen.getByText(/Every token has been decided/)).toBeInTheDocument();
    });

    it('reports progress with skips included, not only issues', async () => {
      stubLoad(
        [record(), record({ key: 'b', name: 'other', disposition: 'to_recreate' })],
        counts({ total: 2, pending: 1, to_recreate: 1 }),
      );

      renderPage();
      expect(await screen.findByText('1 of 2 handled')).toBeInTheDocument();
    });
  });

  describe('issuing', () => {
    it('shows the value once, and says so', async () => {
      const user = userEvent.setup();
      (api.issueToken as Mock).mockResolvedValue({
        token: {
          key: 'st:payments/prd/ci-deploy',
          kind: 'service_token',
          name: 'ci-deploy',
          scope: 'payments/prd',
          access: 'read',
          value: 'dp.st.prd.SECRETVALUE',
          expiry_shortened: false,
          issued_at: '2026-09-18T14:23:48Z',
        },
        counts: counts({ pending: 0, issued: 1 }),
        gate: { counts: counts({ pending: 0, issued: 1 }), blocked: false },
      });

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));

      const dialog = await screen.findByRole('dialog');
      expect(within(dialog).getByText(/shown this once/)).toBeInTheDocument();

      // Hidden until asked for, then shown, then hidden again.
      expect(within(dialog).queryByText('dp.st.prd.SECRETVALUE')).not.toBeInTheDocument();
      expect(within(dialog).getByLabelText('Token value, hidden').textContent).toMatch(/^•+$/);

      await user.click(within(dialog).getByRole('button', { name: 'Show token' }));
      expect(within(dialog).getByText('dp.st.prd.SECRETVALUE')).toBeInTheDocument();
      expect(within(dialog).getByRole('button', { name: 'Hide token' })).toHaveAttribute('aria-pressed', 'true');

      await user.click(within(dialog).getByRole('button', { name: 'Hide token' }));
      expect(within(dialog).queryByText('dp.st.prd.SECRETVALUE')).not.toBeInTheDocument();
    });

    it('copies the real value while it is hidden', async () => {
      // Hiding is for whoever can see the screen; the clipboard still gets the token.
      const user = userEvent.setup();
      const writeText = vi.fn(() => Promise.resolve());
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
      (api.issueToken as Mock).mockResolvedValue({
        token: {
          key: 'st:payments/prd/ci-deploy',
          kind: 'service_token',
          name: 'ci-deploy',
          scope: 'payments/prd',
          value: 'dp.st.prd.SECRETVALUE',
          expiry_shortened: false,
          issued_at: '2026-09-18T14:23:48Z',
        },
        counts: counts({ pending: 0, issued: 1 }),
        gate: { counts: counts({ pending: 0, issued: 1 }), blocked: false },
      });

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));
      await user.click(await screen.findByRole('button', { name: /^Copy$/ }));

      expect(writeText).toHaveBeenCalledWith('dp.st.prd.SECRETVALUE');
      expect(screen.queryByText('dp.st.prd.SECRETVALUE')).not.toBeInTheDocument();
    });

    it('does not give away the length of the token', async () => {
      const user = userEvent.setup();
      (api.issueToken as Mock).mockResolvedValue({
        token: {
          key: 'st:payments/prd/ci-deploy',
          kind: 'service_token',
          name: 'ci-deploy',
          scope: 'payments/prd',
          value: 'x',
          expiry_shortened: false,
          issued_at: '2026-09-18T14:23:48Z',
        },
        counts: counts({ pending: 0, issued: 1 }),
        gate: { counts: counts({ pending: 0, issued: 1 }), blocked: false },
      });

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));
      expect((await screen.findByLabelText('Token value, hidden')).textContent).toHaveLength(96);
    });

    it('will not let the only copy be dismissed by reflex', async () => {
      // Closing without copying destroys something unrecoverable, and nothing
      // afterwards can tell you that is what happened.
      const user = userEvent.setup();
      (api.issueToken as Mock).mockResolvedValue({
        token: {
          key: 'st:payments/prd/ci-deploy',
          kind: 'service_token',
          name: 'ci-deploy',
          scope: 'payments/prd',
          value: 'dp.st.prd.SECRETVALUE',
          expiry_shortened: false,
          issued_at: '2026-09-18T14:23:48Z',
        },
        counts: counts({ pending: 0, issued: 1 }),
        gate: { counts: counts({ pending: 0, issued: 1 }), blocked: false },
      });

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));
      await user.click(await screen.findByRole('button', { name: 'I Have Copied It' }));

      expect(await screen.findByText('Close Without Copying?')).toBeInTheDocument();
      // And the value is still reachable from there.
      expect(screen.getByRole('button', { name: 'Back To The Value' })).toBeInTheDocument();
    });

    it('says when an expiry could not be reproduced', async () => {
      const user = userEvent.setup();
      (api.issueToken as Mock).mockResolvedValue({
        token: {
          key: 'st:payments/prd/ci-deploy',
          kind: 'service_token',
          name: 'ci-deploy',
          scope: 'payments/prd',
          value: 'dp.st.prd.SECRETVALUE',
          expires_at: '2027-09-18T00:00:00Z',
          expiry_shortened: true,
          issued_at: '2026-09-18T14:23:48Z',
        },
        counts: counts({ pending: 0, issued: 1 }),
        gate: { counts: counts({ pending: 0, issued: 1 }), blocked: false },
      });

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));
      expect(await screen.findByText(/issued at the one-year maximum/)).toBeInTheDocument();
    });

    it('never writes the value to browser storage', async () => {
      // The security property the rest of the design rests on. localStorage is
      // the easy accident: it survives the tab, and Claude never sees it go in.
      const user = userEvent.setup();
      (api.issueToken as Mock).mockResolvedValue({
        token: {
          key: 'st:payments/prd/ci-deploy',
          kind: 'service_token',
          name: 'ci-deploy',
          scope: 'payments/prd',
          value: 'dp.st.prd.SECRETVALUE',
          expiry_shortened: false,
          issued_at: '2026-09-18T14:23:48Z',
        },
        counts: counts({ pending: 0, issued: 1 }),
        gate: { counts: counts({ pending: 0, issued: 1 }), blocked: false },
      });

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));
      await screen.findByRole('dialog');

      const dumped = JSON.stringify({ ...localStorage, ...sessionStorage });
      expect(dumped).not.toContain('SECRETVALUE');
    });
  });

  describe('failures', () => {
    it('names the trusted-IP gate rather than repeating the status code', async () => {
      const user = userEvent.setup();
      (api.issueToken as Mock).mockRejectedValue(
        new api.APIError(403, 'the target refused the write: this config’s trusted IPs no longer include the migrator'),
      );

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));

      expect(await screen.findByText('Blocked By Trusted IPs')).toBeInTheDocument();
      // The only honest remedy: the migrator cannot reopen the gate for itself.
      expect(screen.getByRole('button', { name: /Skip And Add To Worklist/ })).toBeInTheDocument();
      expect(screen.getByText(/cannot let itself back in/)).toBeInTheDocument();
    });

    it('offers the worklist as the way out of a failure', async () => {
      const user = userEvent.setup();
      (api.issueToken as Mock).mockRejectedValue(new api.APIError(429, 'rate limited'));
      (api.skipToken as Mock).mockResolvedValue(
        tokensResponse([record({ disposition: 'to_recreate' })], counts({ pending: 0, to_recreate: 1 })),
      );

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Issue Token/ }));
      await user.click(await screen.findByRole('button', { name: /Skip And Add To Worklist/ }));

      await waitFor(() => expect(api.skipToken).toHaveBeenCalledWith('st:payments/prd/ci-deploy', false));
    });
  });

  describe('skipping', () => {
    it('defaults to the case that goes on the worklist', async () => {
      const user = userEvent.setup();
      (api.skipToken as Mock).mockResolvedValue(
        tokensResponse([record({ disposition: 'to_recreate' })], counts({ pending: 0, to_recreate: 1 })),
      );

      renderPage();
      await user.click(await screen.findByRole('button', { name: 'Skip' }));

      expect(await screen.findByRole('radio', { name: /Recreate it by hand/ })).toBeChecked();
      expect(screen.getByRole('radio', { name: /Not needed/ })).not.toBeChecked();

      await user.click(screen.getByRole('button', { name: 'Skip Token' }));
      await waitFor(() => expect(api.skipToken).toHaveBeenCalledWith('st:payments/prd/ci-deploy', false));
    });

    it('keeps a retired token off the worklist when asked', async () => {
      const user = userEvent.setup();
      (api.skipToken as Mock).mockResolvedValue(
        tokensResponse([record({ disposition: 'not_needed' })], counts({ pending: 0, not_needed: 1 })),
      );

      renderPage();
      await user.click(await screen.findByRole('button', { name: 'Skip' }));
      await user.click(await screen.findByRole('radio', { name: /Not needed/ }));
      await user.click(screen.getByRole('button', { name: 'Skip Token' }));

      await waitFor(() => expect(api.skipToken).toHaveBeenCalledWith('st:payments/prd/ci-deploy', true));
    });

    it('clears the whole list in one action', async () => {
      const user = userEvent.setup();
      (api.skipAllTokens as Mock).mockResolvedValue({
        ...tokensResponse([record({ disposition: 'to_recreate' })], counts({ pending: 0, to_recreate: 1 })),
        moved: 1,
      });

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Skip All Remaining/ }));
      const dialog = await screen.findByRole('dialog');
      expect(within(dialog).getByText('Skip All Remaining?')).toBeInTheDocument();

      await user.click(within(dialog).getByRole('button', { name: 'Skip All Remaining' }));
      await waitFor(() => expect(api.skipAllTokens).toHaveBeenCalled());
    });
  });

  describe('rows that cannot simply be issued', () => {
    it('closes an already-expired token without offering to reissue it', async () => {
      // Reissuing would grant access the source no longer has.
      stubLoad(
        [
          record({
            disposition: 'not_needed',
            source_expires_at: '2026-05-30T00:00:00Z',
            note: 'already expired on the source',
          }),
        ],
        counts({ pending: 0, not_needed: 1 }),
      );

      renderPage();
      expect(await screen.findByText('Not needed')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /Issue Token/ })).not.toBeInTheDocument();
      expect(screen.getByText('already expired on the source')).toBeInTheDocument();
    });

    it('offers a reroll, not a second token, when the name already exists', async () => {
      stubLoad([record({ preexisting_on_target: true })], counts());

      renderPage();
      expect(await screen.findByText('On target')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /Reroll/ })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /Issue Token/ })).not.toBeInTheDocument();
    });

    it('promises the source is untouched before rerolling', async () => {
      const user = userEvent.setup();
      stubLoad(
        [record({ disposition: 'issued', issued_at: '2026-09-18T14:22:09Z', target_slug: 's1' })],
        counts({ pending: 0, issued: 1 }),
      );

      renderPage();
      await user.click(await screen.findByRole('button', { name: /Reroll/ }));

      expect(await screen.findByText('Reroll This Token?')).toBeInTheDocument();
      expect(screen.getByText(/source workplace is not touched/)).toBeInTheDocument();
      expect(screen.getByText(/stops working the moment this runs/)).toBeInTheDocument();
    });
  });

  it('separates service account tokens, which have no access of their own', async () => {
    stubLoad(
      [
        record(),
        record({
          key: 'sa:platform/terraform-runner',
          kind: 'service_account_token',
          project: undefined,
          config: undefined,
          service_account: 'c47ce05a-efd6-4770-a71d-bf355d0268e1',
          service_account_name: 'Platform Automation',
          name: 'terraform-runner',
          access: 'admin',
        }),
      ],
      counts({ total: 2, pending: 2 }),
    );

    renderPage();
    expect(await screen.findByText('Service Account Tokens')).toBeInTheDocument();
    // The account by name; its slug is a UUID that means nothing to anyone.
    expect(screen.getByText('Platform Automation')).toBeInTheDocument();
    expect(screen.queryByText('c47ce05a-efd6-4770-a71d-bf355d0268e1')).not.toBeInTheDocument();
  });

  it('reports a failure to load at all, with a way to retry', async () => {
    (api.getTokens as Mock).mockRejectedValue(new Error('nope'));

    renderPage();
    expect(await screen.findByText('nope')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Try Again' })).toBeInTheDocument();
  });

  it('says how many tokens wait for their project to be migrated', async () => {
    // After a partial run, the rest are held back rather than offered: issuing
    // one for a project the target does not have fails.
    (api.getTokens as Mock).mockResolvedValue({ ...tokensResponse([record()], counts()), not_yet_migrated: 3 });
    renderPage();
    expect(await screen.findByText(/3 more tokens belong to projects or service accounts not migrated yet/)).toBeInTheDocument();
  });

  it('sets a token aside for later, and says cutover still waits on it', async () => {
    const user = userEvent.setup();
    const deferred = record({ disposition: 'deferred' });
    (api.deferToken as Mock).mockResolvedValue(tokensResponse([deferred], counts({ pending: 0, deferred: 1 })));
    renderPage();

    await user.click(await screen.findByRole('button', { name: 'Later' }));
    expect(api.deferToken).toHaveBeenCalledWith(record().key);

    // The row says so, with a way back.
    expect(await screen.findByText('Later')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();
    expect(screen.getByText(/1 set aside for later/)).toBeInTheDocument();
    expect(screen.getByText(/come back undecided on the next top-up/)).toBeInTheDocument();
  });

  describe('where it leads', () => {
    const standby = (over: Partial<api.Standby>): api.Standby => ({
      configured: true,
      gathered: true,
      full_done: true,
      in_flight: true,
      waiting: false,
      top_ups: 0,
      runs: [{ kind: 'full', at: '2026-09-22T16:15:00Z', projects: 4, created: 10, updated: 0, failed: 0 }],
      tokens_pending: 0,
      open_errors: 0,
      manual: [],
      ...over,
    });

    it('goes back to waiting while waiting to cut over', async () => {
      (api.getStandby as Mock).mockResolvedValue(standby({ waiting: true }));
      const user = userEvent.setup();
      renderPage();
      await user.click(await screen.findByRole('button', { name: /Back To Waiting/ }));
      expect(mockedNavigate).toHaveBeenCalledWith('/standby');
      expect(screen.queryByRole('button', { name: /Continue To Cutover/ })).not.toBeInTheDocument();
    });

    it('goes to the summary, where the next step is chosen, after a run', async () => {
      // The report: with the rest set aside for later, the only way on was
      // cutover — not the choice between cutting over and waiting.
      (api.getStandby as Mock).mockResolvedValue(standby({ in_flight: false, full_done: false }));
      const user = userEvent.setup();
      renderPage();
      await user.click(await screen.findByRole('button', { name: /^Continue/ }));
      expect(mockedNavigate).toHaveBeenCalledWith('/complete');
    });
  });

  it('does not call tokens set aside decided', async () => {
    (api.getStandby as Mock).mockRejectedValue(new Error('none')); // no run history: a dry run
    stubLoad([record({ disposition: 'deferred' })], counts({ pending: 0, deferred: 1 }));
    renderPage();
    expect(await screen.findByText('Nothing to decide now.')).toBeInTheDocument();
    expect(screen.queryByText('Every token has been decided.')).not.toBeInTheDocument();
    // Without a run history (a dry run), the way on is cutover, which waits on it.
    expect(screen.getByRole('button', { name: /Continue To Cutover/ })).toBeDisabled();
  });
});

