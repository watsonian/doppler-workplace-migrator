import { Component, type ErrorInfo, type ReactNode } from 'react';
import { Button, Callout } from '@/components/ui/primitives';

/**
 * Catches a page that throws while rendering.
 *
 * Without it React unmounts everything, and a crash looks like a white screen
 * with no clue what happened — which is what the cutover page did when it was
 * handed a list as null. This keeps the header and stepper, says the page
 * failed, and shows why, so the report back is the message rather than "it
 * went white". The layout keys it by route, so moving to another page clears
 * it.
 */
export default class PageErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Kept in the console for whoever opens devtools; the page shows the gist.
    console.error('Page failed to render', error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    return (
      <div className="mx-auto w-full max-w-[720px] px-6 py-8">
        <Callout tone="error">
          <span className="font-semibold text-gray-900">This page could not be shown.</span> Nothing
          was changed by it failing, and a run in progress carries on regardless.
          <code className="mt-2 block break-words font-mono text-xs text-gray-700">{error.message}</code>
          <span className="mt-3 flex gap-2">
            <Button variant="secondary" onClick={() => window.location.reload()}>
              Reload Page
            </Button>
            <Button variant="ghost" onClick={() => window.history.back()}>
              Go Back
            </Button>
          </span>
        </Callout>
      </div>
    );
  }
}
