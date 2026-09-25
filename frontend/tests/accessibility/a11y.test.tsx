import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { vi, describe, it, expect, beforeEach } from 'vitest';
import App from '../../src/App';
import { Button, Callout, ProgressBar, TextField } from '../../src/components/ui/primitives';
import FixBox from '../../src/components/errors/FixBox';

// Mock fetch and EventSource for pages that auto-call APIs
const mockFetch = vi.fn(() =>
  Promise.resolve({
    ok: true,
    json: () =>
      Promise.resolve({
        checks: [],
        passed: false,
        service_tokens: [],
        summary: {
          project_count: 0,
          environment_count: 0,
          config_count: 0,
          secret_count: 0,
          role_count: 0,
          group_count: 0,
          webhook_count: 0,
          service_account_count: 0,
          trusted_ip_count: 0,
          manual_step_count: 0,
        },
        checkpoint: null,
        manual_step_count: 0,
      }),
  }),
) as unknown as typeof fetch;
vi.stubGlobal('fetch', mockFetch);

// Implemented as a `function`, not an arrow: subscribeSSE calls `new EventSource(...)`
// and arrow functions are not constructible.
vi.stubGlobal(
  'EventSource',
  vi.fn(function () {
    return {
      addEventListener: vi.fn(),
      close: vi.fn(),
      onerror: null,
    };
  }),
);

function renderApp(route = '/') {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <App />
    </MemoryRouter>,
  );
}

describe('Accessibility', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Interactive elements have proper roles', () => {
    it('buttons render with button role', () => {
      render(<Button>Click me</Button>);
      expect(screen.getByRole('button', { name: 'Click me' })).toBeInTheDocument();
    });

    it('inputs are reachable by their label', () => {
      render(<TextField id="email" label="Email Address" />);
      expect(screen.getByLabelText('Email Address')).toBeInTheDocument();
    });

    it('callouts announce themselves', () => {
      render(<Callout tone="error">Something went wrong</Callout>);
      expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong');
    });

    it('welcome page Get Started button is keyboard accessible', async () => {
      renderApp('/');
      const button = await screen.findByRole('button', { name: 'Get Started' });
      expect(button).not.toBeDisabled();
      expect(button.tagName).toBe('BUTTON');
    });

    it('configure page inputs are reachable by their labels', () => {
      renderApp('/configure');
      // Only the source is present at first — the target card is locked until
      // the source is filled in, so there is one token field, not two.
      const tokenInput = screen.getByLabelText('API token');
      expect(tokenInput.tagName).toBe('INPUT');
      expect(screen.getByLabelText('Instance')).toBeInTheDocument();
    });
  });

  describe('ARIA attributes', () => {
    it('stepper has aria-current on active step', () => {
      renderApp('/configure');
      const stepper = screen.getByRole('navigation', { name: 'Wizard progress' });
      const currentStep = stepper.querySelector('[aria-current="step"]');
      expect(currentStep).not.toBeNull();
    });

    it('stepper step buttons have aria-label with step number and label', () => {
      renderApp('/configure');
      const stepper = screen.getByRole('navigation', { name: 'Wizard progress' });
      const stepButtons = within(stepper).getAllByRole('button');
      // Each step button should have an aria-label
      stepButtons.forEach((btn) => {
        const label = btn.getAttribute('aria-label');
        expect(label).toMatch(/^Step \d+: /);
      });
    });

    it('progress bar reports its position as a percentage', () => {
      render(<ProgressBar value={30} label="Gather progress" />);
      const progressbar = screen.getByRole('progressbar', { name: 'Gather progress' });
      expect(progressbar).toHaveAttribute('aria-valuenow', '30');
      expect(progressbar).toHaveAttribute('aria-valuemin', '0');
      expect(progressbar).toHaveAttribute('aria-valuemax', '100');
    });

    it('masks the token field on the configuration page', () => {
      // A token on screen is a token in a screenshot, in a screen share, and in
      // whatever sits behind the person running the migration.
      //
      // Asserted against the real page rather than against TextField with a type
      // passed in: that only proves the prop reaches the input, which stayed true
      // while two of the three call sites were rendering tokens in plain text.
      renderApp('/configure');
      expect(screen.getByLabelText('API token')).toHaveAttribute('type', 'password');
    });
  });

  describe('Heading hierarchy', () => {
    it('welcome page has h1', async () => {
      renderApp('/');
      const headings = await screen.findAllByRole('heading', { level: 1 });
      expect(headings.length).toBeGreaterThanOrEqual(1);
    });

    it('configure page has h1', () => {
      renderApp('/configure');
      const headings = screen.getAllByRole('heading', { level: 1 });
      expect(headings.length).toBeGreaterThanOrEqual(1);
    });

    it('preflight page has h1', () => {
      renderApp('/preflight');
      const headings = screen.getAllByRole('heading', { level: 1 });
      expect(headings.length).toBeGreaterThanOrEqual(1);
    });

    it('no heading levels are skipped on welcome page', async () => {
      renderApp('/');
      await screen.findByRole('heading', { level: 1 });
      const allHeadings = screen.getAllByRole('heading');
      const levels = allHeadings.map((h) => {
        const tag = h.tagName.toLowerCase();
        return parseInt(tag.replace('h', ''), 10);
      });

      // Verify h1 exists
      expect(levels).toContain(1);

      // Verify no skipped levels: for each level > 1, the previous level must exist
      const uniqueLevels = [...new Set(levels)].sort();
      for (let i = 1; i < uniqueLevels.length; i++) {
        const gap = uniqueLevels[i] - uniqueLevels[i - 1];
        expect(gap).toBeLessThanOrEqual(1);
      }
    });
  });

  describe('Focus management', () => {
    it('disabled buttons cannot be focused via tab', () => {
      render(<Button disabled>Disabled</Button>);
      const btn = screen.getByRole('button', { name: 'Disabled' });
      expect(btn).toBeDisabled();
    });

    it('configure page Continue is disabled when empty', () => {
      renderApp('/configure');
      expect(screen.getByRole('button', { name: /Continue/ })).toBeDisabled();
    });
  });

  describe('Token entry never renders a token in the clear', () => {
    // One test per call site. A shared assertion over a component that takes the
    // type as a prop is what let this regress in the first place.
    it('masks the replacement-token field in the fix box', () => {
      render(
        <FixBox
          remedy={{ kind: 'token', label: 'Supply Token And Re-run', detail: 'Supply a token.' }}
          onApply={() => {}}
        />,
      );
      expect(screen.getByLabelText('Replacement token')).toHaveAttribute('type', 'password');
    });

  });
});