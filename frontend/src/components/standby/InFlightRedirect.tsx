import type { ReactNode } from 'react';
import { Navigate } from 'react-router-dom';
import { isInFlight, useStandby } from '@/hooks/useStandby';

/**
 * One migration at a time. While a full migration is waiting to cut over, the
 * pages that would start another — the welcome screen, and Scope, where a
 * partial run is chosen — open the waiting page instead, with a note saying
 * why, so the redirect does not read as the app ignoring where you went.
 */
export function InFlightRedirect({ children, reason }: { children: ReactNode; reason: string }) {
  const { standby, loaded } = useStandby();
  // Nothing until the answer is in: showing the page and then snatching it
  // away is worse than a moment of blank.
  if (!loaded) return null;
  if (isInFlight(standby)) {
    return <Navigate to="/standby" replace state={{ flash: reason }} />;
  }
  return <>{children}</>;
}
