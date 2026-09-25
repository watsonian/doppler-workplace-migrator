import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect } from 'vitest';
import { WizardProvider } from '@/hooks/useWizard';
import WizardStepper from '@/components/wizard/WizardStepper';

function renderAt(route: string) {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <WizardProvider>
        <WizardStepper />
      </WizardProvider>
    </MemoryRouter>,
  );
}

describe('WizardStepper', () => {
  it('renders one control per step', () => {
    renderAt('/preflight');
    expect(screen.getByRole('navigation', { name: 'Wizard progress' })).toBeInTheDocument();
    expect(screen.getAllByRole('button')).toHaveLength(7);
  });

  it('marks the current step', () => {
    renderAt('/gathering');
    expect(screen.getByRole('button', { name: 'Step 3: Gather' })).toHaveAttribute('aria-current', 'step');
  });

  it('disables steps not yet reached', () => {
    // The migrator is linear: a stepper that lets you jump forward is offering
    // something the tool cannot honour.
    renderAt('/preflight');
    expect(screen.getByRole('button', { name: 'Step 5: Review' })).toBeDisabled();
  });

  it('enables steps already visited', () => {
    renderAt('/gathering');
    expect(screen.getByRole('button', { name: 'Step 1: Configure' })).not.toBeDisabled();
  });

  it('disables the current step so it cannot navigate to itself', () => {
    renderAt('/gathering');
    expect(screen.getByRole('button', { name: 'Step 3: Gather' })).toBeDisabled();
  });

  it('states the position as a sentence for narrow screens', () => {
    // Seven labelled circles do not fit a phone; an unreadable stepper is worse
    // than none, so the position is given in words instead.
    renderAt('/configure');
    expect(screen.getByText('Step 1 of 7 — Configure')).toBeInTheDocument();
  });

  it('counts triage as the migrate step', () => {
    renderAt('/migrate/errors');
    expect(screen.getByText('Step 6 of 7 — Migrate')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Step 6: Migrate' })).toHaveAttribute('aria-current', 'step');
  });

  it('shows the last step as the summary', () => {
    renderAt('/complete');
    expect(screen.getByText('Step 7 of 7 — Summary')).toBeInTheDocument();
  });
});
