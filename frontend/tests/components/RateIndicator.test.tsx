import { act, render, screen } from '@testing-library/react';
import { vi, type Mock } from 'vitest';
import { RateBadge, RatePanel, levelOf, useRateState } from '../../src/components/RateIndicator';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getRateLimits: vi.fn(),
  subscribeSSE: vi.fn(),
}));

let emit: (type: string, data: unknown) => void = () => {};

function Harness() {
  const rates = useRateState();
  return (
    <div>
      <span data-testid="source-level">{levelOf(rates.source)}</span>
      <span data-testid="target-level">{levelOf(rates.target)}</span>
      <div data-testid="target-badge">
        <RateBadge state={rates.target} />
      </div>
      <RatePanel sides={rates} hosts={{ source: 'api.doppler.com', target: 'doppler.internal' }} />
    </div>
  );
}

const inAMinute = () => new Date(Date.now() + 60_000).toISOString();

function side(s: 'source' | 'target', used: number, level: string) {
  return {
    side: s,
    level,
    used,
    reset_at: inAMinute(),
    categories: [{ category: 'read', remaining: Math.round(480 * (1 - used)), limit: 480, used, reset_at: inAMinute() }],
  };
}

describe('the rate limit indicator', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.subscribeSSE as Mock).mockImplementation((handler: typeof emit) => {
      emit = handler;
      return { unsubscribe: vi.fn(), connected: Promise.resolve() };
    });
  });

  it('is grey before any request, then follows each token as responses arrive', async () => {
    (api.getRateLimits as Mock).mockResolvedValue({ waits: [], status: [] });
    render(<Harness />);
    expect(screen.getByTestId('source-level')).toHaveTextContent('');

    act(() => emit('rate_status', [side('source', 0.2, 'ok'), side('target', 0.85, 'warn')]));
    expect(screen.getByTestId('source-level')).toHaveTextContent('ok');
    expect(screen.getByTestId('target-level')).toHaveTextContent('warn');
    expect(screen.getByTestId('target-badge')).toHaveTextContent('85%');
    expect(screen.getByText('Nearing a limit')).toBeInTheDocument();
  });

  it('turns red on a pause, and says when requests resume', async () => {
    (api.getRateLimits as Mock).mockResolvedValue({ waits: [], status: [] });
    render(<Harness />);
    act(() => emit('rate_status', [side('target', 0.4, 'ok')]));

    const resume = inAMinute();
    act(() => emit('rate_limit', { side: 'target', category: 'write', waiting: true, resume_at: resume, refused: true }));
    expect(screen.getByTestId('target-level')).toHaveTextContent('limited');
    expect(screen.getByTestId('target-badge')).toHaveTextContent('resets');
    expect(screen.getByText(/requests resume at/)).toBeInTheDocument();

    act(() => emit('rate_limit', { side: 'target', category: 'write', waiting: false, resume_at: resume, refused: true }));
    expect(screen.getByTestId('target-level')).toHaveTextContent('ok');
  });

  it('shows a spent limit from the headers as red, with its reset', async () => {
    (api.getRateLimits as Mock).mockResolvedValue({ waits: [], status: [side('source', 1, 'limited')] });
    render(<Harness />);
    expect(await screen.findByText(/Rate limited — resets at/)).toBeInTheDocument();
    expect(screen.getByTestId('source-level')).toHaveTextContent('limited');
  });
});
