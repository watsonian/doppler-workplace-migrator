import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, it, expect, vi } from 'vitest';
import { WizardProvider, useWizard } from '../../src/hooks/useWizard';

function WizardDisplay() {
  const {
    steps,
    currentStepIndex,
    furthestStepIndex,
    totalSteps,
    goToStep,
    stepless,
    hasBlockingError,
    setBlockingError,
  } = useWizard();
  const location = useLocation();
  return (
    <div>
      <span data-testid="current">{currentStepIndex}</span>
      <span data-testid="furthest">{furthestStepIndex}</span>
      <span data-testid="total">{totalSteps}</span>
      <span data-testid="path">{location.pathname}</span>
      <span data-testid="step-count">{steps.length}</span>
      <span data-testid="stepless">{String(stepless)}</span>
      <span data-testid="blocking">{String(hasBlockingError)}</span>
      <button data-testid="go-0" onClick={() => goToStep(0)}>Go 0</button>
      <button data-testid="go-2" onClick={() => goToStep(2)}>Go 2</button>
      <button data-testid="go-5" onClick={() => goToStep(5)}>Go 5</button>
      <button data-testid="set-blocking" onClick={() => setBlockingError(true)}>Block</button>
    </div>
  );
}

function renderWithRoute(route: string) {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <WizardProvider>
        <WizardDisplay />
      </WizardProvider>
    </MemoryRouter>,
  );
}

describe('WizardProvider + useWizard', () => {
  it('derives the current step from the URL', () => {
    renderWithRoute('/gathering');
    expect(screen.getByTestId('current').textContent).toBe('2');
  });

  it('exposes the seven steps', () => {
    renderWithRoute('/configure');
    expect(screen.getByTestId('total').textContent).toBe('7');
    expect(screen.getByTestId('step-count').textContent).toBe('7');
  });

  it('sets furthest to current on first render', () => {
    renderWithRoute('/plan');
    expect(screen.getByTestId('furthest').textContent).toBe('4');
  });

  it('navigates back to a step already visited', async () => {
    const user = userEvent.setup();
    renderWithRoute('/gathering');
    expect(screen.getByTestId('current').textContent).toBe('2');

    await user.click(screen.getByTestId('go-0'));
    expect(screen.getByTestId('path').textContent).toBe('/configure');
  });

  it('refuses to jump ahead of the furthest step reached', async () => {
    const user = userEvent.setup();
    // The migrator is a linear job. Offering a jump forward would promise
    // something the tool cannot honour — the later step has no data yet.
    renderWithRoute('/configure');
    expect(screen.getByTestId('furthest').textContent).toBe('0');

    await user.click(screen.getByTestId('go-5'));
    expect(screen.getByTestId('path').textContent).toBe('/configure');
  });

  it('treats the welcome screen as outside the wizard', () => {
    renderWithRoute('/');
    expect(screen.getByTestId('stepless').textContent).toBe('true');
  });

  it('keeps triage on the migrate step', () => {
    renderWithRoute('/migrate/errors');
    expect(screen.getByTestId('current').textContent).toBe('5');
    expect(screen.getByTestId('stepless').textContent).toBe('false');
  });

  it('carries a blocking error for the current step', async () => {
    const user = userEvent.setup();
    renderWithRoute('/preflight');
    expect(screen.getByTestId('blocking').textContent).toBe('false');

    await user.click(screen.getByTestId('set-blocking'));
    expect(screen.getByTestId('blocking').textContent).toBe('true');
  });

  it('returns the first step for an unknown path', () => {
    renderWithRoute('/nonexistent');
    expect(screen.getByTestId('current').textContent).toBe('0');
  });

  it('throws when used outside WizardProvider', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    expect(() => {
      render(
        <MemoryRouter>
          <WizardDisplay />
        </MemoryRouter>,
      );
    }).toThrow('useWizard must be used within a WizardProvider');
    spy.mockRestore();
  });
});
