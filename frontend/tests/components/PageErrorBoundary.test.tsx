import { render, screen } from '@testing-library/react';
import { vi } from 'vitest';
import PageErrorBoundary from '../../src/components/PageErrorBoundary';

function Broken(): never {
  throw new Error("Cannot read properties of null (reading 'length')");
}

describe('PageErrorBoundary', () => {
  it('shows what failed instead of a blank screen', () => {
    // React logs the caught error itself; keep the test output quiet.
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <PageErrorBoundary>
        <Broken />
      </PageErrorBoundary>,
    );
    spy.mockRestore();

    expect(screen.getByText('This page could not be shown.')).toBeInTheDocument();
    expect(screen.getByText(/reading 'length'/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reload Page' })).toBeInTheDocument();
  });

  it('stays out of the way when nothing fails', () => {
    render(
      <PageErrorBoundary>
        <p>fine</p>
      </PageErrorBoundary>,
    );
    expect(screen.getByText('fine')).toBeInTheDocument();
  });
});
