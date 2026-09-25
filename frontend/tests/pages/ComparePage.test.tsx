import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import ComparePage from '../../src/pages/ComparePage';
import { WizardProvider } from '../../src/hooks/useWizard';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getCompare: vi.fn(),
  startCompare: vi.fn(),
  subscribeSSE: vi.fn(() => ({ unsubscribe: vi.fn(), connected: Promise.resolve() })),
  getStandby: vi.fn(() => Promise.reject(new Error('none'))),
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <WizardProvider>
        <ComparePage />
      </WizardProvider>
    </MemoryRouter>,
  );
}

describe('ComparePage', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows a comparison that found nothing, sent with null lists', async () => {
    // The first top-up after a full migration: nothing had changed, the server
    // sent differences as null, and the page crashed looping over it.
    (api.getCompare as Mock).mockResolvedValue({
      status: 'complete',
      counts: { create: 0, update: 0, match: 64, orphan: 0, drift: 0 },
      target_empty: false,
      differences: null,
      matched: { secret: 64 },
    });
    renderPage();

    expect(await screen.findByText('Secrets')).toBeInTheDocument();
    expect(screen.getByText('64 already current')).toBeInTheDocument();
    expect(screen.queryByText(/could not be shown/)).not.toBeInTheDocument();
  });

  it('takes a dry run’s empty answer as finished, not as still running', async () => {
    (api.getCompare as Mock).mockRejectedValue(new Error('none yet'));
    (api.startCompare as Mock).mockResolvedValue({
      status: 'complete',
      counts: { create: 0, update: 0, match: 0, orphan: 0, drift: 0 },
      target_empty: true,
      differences: null,
      matched: null,
    });
    renderPage();

    expect(await screen.findByRole('button', { name: /Continue/ })).toBeEnabled();
  });
});
