import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { useSSE } from '../../src/hooks/useSSE';

// Mock the subscribeSSE function
const mockUnsub = vi.fn();
vi.mock('../../src/api/client', () => ({
  subscribeSSE: vi.fn(() => ({ unsubscribe: mockUnsub, connected: Promise.resolve() })),
}));

import { subscribeSSE } from '../../src/api/client';
const mockSubscribeSSE = vi.mocked(subscribeSSE);

describe('useSSE', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('connect calls subscribeSSE with the handler', () => {
    const { result } = renderHook(() => useSSE());
    const handler = vi.fn();

    act(() => {
      result.current.connect(handler);
    });

    expect(mockSubscribeSSE).toHaveBeenCalledWith(handler);
  });

  it('disconnect calls the unsubscribe function', () => {
    const { result } = renderHook(() => useSSE());
    const handler = vi.fn();

    act(() => {
      result.current.connect(handler);
    });

    act(() => {
      result.current.disconnect();
    });

    expect(mockUnsub).toHaveBeenCalledTimes(1);
  });

  it('double connect disconnects the first before connecting again', () => {
    const { result } = renderHook(() => useSSE());
    const handler1 = vi.fn();
    const handler2 = vi.fn();

    act(() => {
      result.current.connect(handler1);
    });
    act(() => {
      result.current.connect(handler2);
    });

    // First connection should have been closed
    expect(mockUnsub).toHaveBeenCalledTimes(1);
    // subscribeSSE called twice
    expect(mockSubscribeSSE).toHaveBeenCalledTimes(2);
  });

  it('auto-cleans up on unmount', () => {
    const { result, unmount } = renderHook(() => useSSE());
    const handler = vi.fn();

    act(() => {
      result.current.connect(handler);
    });

    unmount();
    expect(mockUnsub).toHaveBeenCalledTimes(1);
  });

  it('disconnect is safe to call without prior connect', () => {
    const { result } = renderHook(() => useSSE());

    act(() => {
      result.current.disconnect();
    });

    // Should not throw — mockUnsub should not be called
    expect(mockUnsub).not.toHaveBeenCalled();
  });
});
