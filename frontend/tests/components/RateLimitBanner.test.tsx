import { act, render, screen } from '@testing-library/react';
import { vi, type Mock } from 'vitest';
import RateLimitBanner from '../../src/components/RateLimitBanner';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getRateLimits: vi.fn(),
  subscribeSSE: vi.fn(),
}));

let emit: (type: string, data: unknown) => void = () => {};

describe('RateLimitBanner', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2026-09-22T14:31:24Z'));
    (api.getRateLimits as Mock).mockResolvedValue({ waits: [] });
    (api.subscribeSSE as Mock).mockImplementation((handler: typeof emit) => {
      emit = handler;
      return { unsubscribe: vi.fn(), connected: Promise.resolve() };
    });
  });

  afterEach(() => vi.useRealTimers());

  it('shows nothing when nothing is limited', async () => {
    const { container } = render(<RateLimitBanner />);
    await act(async () => {});
    expect(container).toBeEmptyDOMElement();
  });

  it('says which side, what kind of request, and when it resumes', async () => {
    render(<RateLimitBanner />);
    await act(async () => {
      emit('rate_limit', {
        side: 'source',
        category: 'secret_read',
        waiting: true,
        resume_at: '2026-09-22T14:32:05Z',
        refused: false,
      });
    });

    expect(screen.getByRole('status')).toHaveTextContent(/Paused by the source.s rate limit/);
    expect(screen.getByRole('status')).toHaveTextContent('secret reads');
    expect(screen.getByText(/Resumes at .* · in 41s/)).toBeInTheDocument();
  });

  it('counts down, and says when the time is up before the event lands', async () => {
    render(<RateLimitBanner />);
    await act(async () => {
      emit('rate_limit', { side: 'target', category: 'write', waiting: true, resume_at: '2026-09-22T14:31:27Z', refused: true });
    });
    expect(screen.getByText(/in 3s/)).toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.getByText(/in 1s/)).toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.getByText('Resuming now…')).toBeInTheDocument();
  });

  it('tells a refusal apart from a spent allowance', async () => {
    render(<RateLimitBanner />);
    await act(async () => {
      emit('rate_limit', { side: 'target', category: 'write', waiting: true, resume_at: '2026-09-22T14:33:00Z', refused: true });
    });
    expect(screen.getByRole('status')).toHaveTextContent('asked the migrator to slow down');
    expect(screen.getByText(/in 1m 36s/)).toBeInTheDocument();
  });

  it('clears when the pause ends', async () => {
    const { container } = render(<RateLimitBanner />);
    await act(async () => {
      emit('rate_limit', { side: 'source', category: 'read', waiting: true, resume_at: '2026-09-22T14:32:00Z', refused: false });
    });
    expect(screen.getByRole('status')).toBeInTheDocument();

    await act(async () => {
      emit('rate_limit', { side: 'source', category: 'read', waiting: false, resume_at: '0001-01-01T00:00:00Z', refused: false });
    });
    expect(container).toBeEmptyDOMElement();
  });

  it('shows both sides when both are limiting', async () => {
    render(<RateLimitBanner />);
    await act(async () => {
      emit('rate_limit', { side: 'source', category: 'secret_read', waiting: true, resume_at: '2026-09-22T14:32:05Z', refused: false });
      emit('rate_limit', { side: 'target', category: 'write', waiting: true, resume_at: '2026-09-22T14:31:50Z', refused: true });
    });
    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(/source.s rate limit/);
    expect(status).toHaveTextContent(/target.s rate limit/);
  });

  it('picks up a pause already in progress when the page opens', async () => {
    (api.getRateLimits as Mock).mockResolvedValue({
      waits: [{ side: 'source', category: 'secret_read', resume_at: '2026-09-22T14:32:05Z', refused: false }],
    });
    render(<RateLimitBanner />);
    expect(await screen.findByText(/in 41s/)).toBeInTheDocument();
  });

  it('drops a pause whose end was missed, a few seconds after it was due', async () => {
    // The initial fetch can race the stream and restore a pause that had just
    // ended. It must not sit at "Resuming now…" for good.
    const { container } = render(<RateLimitBanner />);
    await act(async () => {
      emit('rate_limit', { side: 'source', category: 'read', waiting: true, resume_at: '2026-09-22T14:31:26Z', refused: false });
    });
    await act(async () => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.getByText('Resuming now…')).toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(6000);
    });
    expect(container).toBeEmptyDOMElement();
  });
});
