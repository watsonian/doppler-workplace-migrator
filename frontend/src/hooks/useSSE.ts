import { useRef, useCallback, useEffect } from 'react';
import { subscribeSSE, type SSEEventHandler } from '@/api/client';

export function useSSE() {
  const unsubRef = useRef<(() => void) | null>(null);

  const connect = useCallback((onEvent: SSEEventHandler) => {
    // Disconnect any existing connection first
    if (unsubRef.current) {
      unsubRef.current();
      unsubRef.current = null;
    }
    const sse = subscribeSSE(onEvent);
    unsubRef.current = sse.unsubscribe;
    return sse.connected;
  }, []);

  const disconnect = useCallback(() => {
    if (unsubRef.current) {
      unsubRef.current();
      unsubRef.current = null;
    }
  }, []);

  // Auto-cleanup on unmount
  useEffect(() => {
    return () => {
      if (unsubRef.current) {
        unsubRef.current();
        unsubRef.current = null;
      }
    };
  }, []);

  return { connect, disconnect };
}

/**
 * Subscribes to one kind of server event for the lifetime of a component.
 *
 * The `useSSE` hook above hands out a connection to drive by hand, which suits a
 * page that runs one long operation. A view that merely needs to stay current —
 * triage, while the run it is describing keeps going — wants this instead.
 *
 * The handler is held in a ref so that passing an inline function does not tear
 * the connection down and rebuild it on every render.
 */
export function useSSEEvent(type: string, handler: (data: unknown) => void) {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    const sub = subscribeSSE((eventType, data) => {
      if (eventType === type) handlerRef.current(data);
    });
    return sub.unsubscribe;
  }, [type]);
}
