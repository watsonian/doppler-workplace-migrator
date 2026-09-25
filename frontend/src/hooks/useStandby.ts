import { useCallback, useEffect, useState } from 'react';
import { getStandby } from '@/api/client';
import type { Standby } from '@/api/client';

/**
 * Where the migration stands between its full run and cutover. While one is in
 * flight, a full-scope run is a top-up, and the pages it passes through route
 * back to the waiting page rather than to Scope.
 *
 * Null until loaded, and left null if it cannot be read: every page is still
 * correct as an ordinary run without it. `loaded` says the question has been
 * answered either way, for a redirect that must not wait forever.
 */
export function useStandby(): { standby: Standby | null; loaded: boolean; refresh: () => Promise<void> } {
  const [standby, setStandby] = useState<Standby | null>(null);
  const [loaded, setLoaded] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setStandby(await getStandby());
    } catch {
      // Not knowing is the ordinary run.
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { standby, loaded, refresh };
}

/** True from the end of a full migration until it is cut over. */
export function isInFlight(standby: Standby | null): boolean {
  return Boolean(standby?.in_flight);
}
