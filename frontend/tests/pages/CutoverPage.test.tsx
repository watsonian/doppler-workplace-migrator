import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import CutoverPage from '../../src/pages/CutoverPage';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  planCutover: vi.fn(),
  startCutover: vi.fn(),
  getErrors: vi.fn(() => Promise.resolve({ total: 0, open: 0, classes: [] })),
  getStandby: vi.fn(() => Promise.reject(new Error('none'))),
  setStandby: vi.fn(() => Promise.resolve({})),
  errorsExportURL: '/api/v1/errors/export.csv',
  finalReportURL: '/api/v1/report/final.html',
  finalReportJSONURL: '/api/v1/report/final.json',
  tokenWorklistURL: '/api/v1/tokens/worklist.csv',
  subscribeSSE: vi.fn(() => ({ unsubscribe: vi.fn(), connected: Promise.resolve() })),
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <CutoverPage />
    </MemoryRouter>,
  );
}

// One config to mirror, so the page has work to offer and the button would
// otherwise be live.
const PLAN = {
  trusted_ips: [{ project: 'payments', config: 'prd', scope: 'payments/prd', add: ['10.0.0.0/8'], closes_config: false }],
  already_matching: 0,
  matching: [],
  unreadable: [],
  closing_count: 0,
};

describe('CutoverPage', () => {
  beforeEach(() => vi.clearAllMocks());

  it('waits for a full migration after a partial one', async () => {
    (api.planCutover as Mock).mockResolvedValue({ status: 'planned', plan: PLAN, partial_only: true });
    renderPage();

    expect(await screen.findByText('Cutover needs a full migration first.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Cut Over/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Choose A Full Migration' })).toBeInTheDocument();
  });

  it('is available once the target holds everything', async () => {
    (api.planCutover as Mock).mockResolvedValue({ status: 'planned', plan: PLAN, partial_only: false });
    renderPage();

    expect(await screen.findByRole('button', { name: /^Cut Over/ })).toBeEnabled();
    expect(screen.queryByText(/needs a full migration first/)).not.toBeInTheDocument();
  });

  it('renders a plan whose lists came back as null', async () => {
    // A workplace with nothing to mirror got null for its lists, and the page,
    // reading .length, went blank.
    (api.planCutover as Mock).mockResolvedValue({
      status: 'planned',
      plan: { trusted_ips: null, matching: null, unreadable: null, already_matching: 0, closing_count: 0 },
      partial_only: false,
    });
    renderPage();
    expect(await screen.findByText(/Nothing to cut over/)).toBeInTheDocument();
  });

  it('lists the configs that already match, not just their count', async () => {
    (api.planCutover as Mock).mockResolvedValue({
      status: 'planned',
      plan: {
        ...PLAN,
        already_matching: 2,
        matching: ['api/dev', 'migrator/dev'],
        unreadable: ['billing/prd'],
      },
      partial_only: false,
    });
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByText('Configs already matching'));
    expect(screen.getByText('api/dev')).toBeInTheDocument();
    expect(screen.getByText('migrator/dev')).toBeInTheDocument();
    expect(screen.getByText(/could not be read from the target/)).toBeInTheDocument();
  });

  it('offers the report for the whole migration, final once nothing is left to cut over', async () => {
    (api.planCutover as Mock).mockResolvedValue({
      status: 'planned',
      plan: { trusted_ips: [], matching: ['api/dev'], unreadable: [], already_matching: 1, closing_count: 0 },
      partial_only: false,
    });
    (api.getErrors as Mock).mockResolvedValue({ total: 5, open: 0, classes: [] });
    renderPage();

    expect(await screen.findByRole('button', { name: /Download Report/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /As JSON/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Token Worklist CSV/ })).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: /Errors As CSV/ })).toBeInTheDocument();
    expect(screen.getByText('Final')).toBeInTheDocument();
  });

  it('finishes a migration whose cutover has nothing to change', async () => {
    (api.planCutover as Mock).mockResolvedValue({
      status: 'planned',
      plan: { trusted_ips: [], matching: ['api/dev'], unreadable: [], already_matching: 1, closing_count: 0 },
      partial_only: false,
    });
    (api.getStandby as Mock).mockResolvedValue({ full_done: true, waiting: true, runs: [], manual: [] });
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole('button', { name: /Finish Migration/ }));
    expect(api.setStandby).toHaveBeenCalledWith('finish');
  });

  it('keeps Cut Over disabled until it knows whether cutover is allowed', async () => {
    // Enabled while the plan loaded and disabled once it arrived, the button
    // invited a quick click on a cutover the page was about to forbid.
    (api.planCutover as Mock).mockImplementation(() => new Promise(() => {}));
    renderPage();
    expect(screen.getByRole('button', { name: /^Cut Over/ })).toBeDisabled();
  });

  it('counts a token set aside for later as undecided', async () => {
    const c = { total: 2, issued: 1, to_recreate: 0, not_needed: 0, pending: 0, deferred: 1 };
    (api.planCutover as Mock).mockResolvedValue({
      status: 'planned',
      plan: PLAN,
      partial_only: false,
      token_gate: { counts: c, blocked: true },
    });
    renderPage();
    expect(await screen.findByText(/1 service token is still undecided \(one set aside for later\)/)).toBeInTheDocument();
    expect(screen.queryByText(/0 service tokens/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Cut Over/ })).toBeDisabled();
  });
});

