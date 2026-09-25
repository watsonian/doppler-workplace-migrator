import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import PlanReviewPage from '../../src/pages/PlanReviewPage';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getCompare: vi.fn(),
  getPlan: vi.fn(() => Promise.resolve({})),
  getScope: vi.fn(),
  setRunOptions: vi.fn(() => Promise.resolve({})),
  recordNoChangeCheck: vi.fn(() => Promise.resolve({})),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

const compare = (create: number, update: number) => ({
  status: 'complete',
  counts: { create, update, match: 19301, orphan: 0, drift: 0 },
  target_empty: false,
  differences: update
    ? [{ kind: 'secret', disposition: 'update', project: 'payments', config: 'prd', name: 'KEY', scope: 'payments/prd', changes: [{ field: 'value', was: '', now: '' }] }]
    : [],
  matched: {},
});

function renderPage() {
  return render(
    <MemoryRouter>
      <PlanReviewPage />
    </MemoryRouter>,
  );
}

describe('Review during a top-up', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getScope as Mock).mockResolvedValue({
      scope: { mode: 'full' },
      run: { resolution: { projects: ['payments'] }, previously_migrated: ['payments'] },
      projects: [{ slug: 'payments' }],
      cutover_blocked: false,
      top_up: { number: 3, since: '2026-09-23T14:02:00Z' },
    });
  });

  it('says which top-up this is and runs it as one', async () => {
    (api.getCompare as Mock).mockResolvedValue(compare(0, 1));
    renderPage();
    expect(await screen.findByText('Top-up 3')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: /^Top Up/ })).toBeInTheDocument();
  });

  it('records a check that found nothing, and goes back to waiting', async () => {
    (api.getCompare as Mock).mockResolvedValue(compare(0, 0));
    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText(/Nothing has changed since the last top-up/)).toBeInTheDocument();
    const back = screen.getAllByRole('button', { name: /Back To Waiting/ }).at(-1)!;
    await user.click(back);
    expect(api.recordNoChangeCheck).toHaveBeenCalled();
    await waitFor(() => expect(mockedNavigate).toHaveBeenCalledWith('/standby'));
    expect(api.setRunOptions).not.toHaveBeenCalled();
  });

  it('shows a visibility change as it was and will be, and never a value', async () => {
    (api.getCompare as Mock).mockResolvedValue({
      ...compare(0, 1),
      differences: [
        {
          kind: 'secret', disposition: 'update', project: 'payments', config: 'prd', name: 'KEY', scope: 'payments/prd',
          changes: [
            { field: 'value', was: '', now: '' },
            { field: 'visibility', was: 'masked', now: 'restricted' },
          ],
        },
      ],
    });
    renderPage();
    // Open by default: a rollup of a few items is shown rather than folded.
    expect(await screen.findByText('masked')).toBeInTheDocument();
    expect(screen.getByText('restricted')).toBeInTheDocument();
    expect(screen.getAllByText('not shown')).toHaveLength(2);
  });
});
