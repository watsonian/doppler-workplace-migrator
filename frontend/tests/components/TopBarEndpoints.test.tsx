import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import WizardLayout from '../../src/components/wizard/WizardLayout';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getConfiguration: vi.fn(),
  getRateLimits: vi.fn(() => Promise.resolve({ source: null, target: null })),
  subscribeSSE: vi.fn(() => ({ unsubscribe: vi.fn(), connected: Promise.resolve() })),
}));

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route element={<WizardLayout />}>
          <Route path="/standby" element={<p>page</p>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe('the top bar', () => {
  it('names the configured source and target', async () => {
    // Never filled in before: nothing wrote the hosts where the bar looked.
    (api.getConfiguration as Mock).mockResolvedValue({
      configured: true,
      source_host: 'https://api.doppler.com',
      target_host: 'https://doppler.internal.example.com/',
    });
    renderAt('/standby');
    expect(await screen.findByText('api.doppler.com')).toBeInTheDocument();
    expect(screen.getByText('doppler.internal.example.com')).toBeInTheDocument();
  });

  it('shows placeholders before anything is configured', async () => {
    (api.getConfiguration as Mock).mockResolvedValue({ configured: false });
    renderAt('/standby');
    expect(await screen.findByText('source')).toBeInTheDocument();
    expect(screen.getByText('target')).toBeInTheDocument();
  });
});
