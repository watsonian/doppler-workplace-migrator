import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import { InFlightRedirect } from '../../src/components/standby/InFlightRedirect';
import FlashBanner from '../../src/components/FlashBanner';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({ getStandby: vi.fn() }));

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <FlashBanner />
      <Routes>
        <Route
          path="/"
          element={
            <InFlightRedirect reason="A migration is waiting to cut over, so it opens here.">
              <p>Welcome</p>
            </InFlightRedirect>
          }
        />
        <Route path="/standby" element={<p>Waiting page</p>} />
        <Route path="/elsewhere" element={<p>Elsewhere</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('one migration at a time', () => {
  beforeEach(() => vi.clearAllMocks());

  it('opens the waiting page instead of the index, and says why', async () => {
    (api.getStandby as Mock).mockResolvedValue({ in_flight: true });
    renderAt('/');

    expect(await screen.findByText('Waiting page')).toBeInTheDocument();
    expect(screen.queryByText('Welcome')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('waiting to cut over');
  });

  it('lets the note be dismissed', async () => {
    (api.getStandby as Mock).mockResolvedValue({ in_flight: true });
    const user = userEvent.setup();
    renderAt('/');

    await user.click(await screen.findByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.getByText('Waiting page')).toBeInTheDocument();
  });

  it('shows the index when nothing is in flight', async () => {
    (api.getStandby as Mock).mockResolvedValue({ in_flight: false });
    renderAt('/');
    expect(await screen.findByText('Welcome')).toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('shows the index when the answer cannot be had', async () => {
    (api.getStandby as Mock).mockRejectedValue(new Error('down'));
    renderAt('/');
    expect(await screen.findByText('Welcome')).toBeInTheDocument();
  });
});
