import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import ManualStepsPage from '../../src/pages/ManualStepsPage';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  getManualSteps: vi.fn(),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <ManualStepsPage />
    </MemoryRouter>,
  );
}

const fullResponse: api.ManualStepsResponse = {
  manual_steps: [
    {
      category: 'integration',
      title: 'Integrations',
      description: 'Integration credentials are redacted in the API and cannot be automatically migrated.',
      items: [
        {
          name: 'AWS SM',
          detail: 'Recreate on target instance with new credentials',
          context: { type: 'aws_secrets_manager', enabled: 'true' },
        },
        {
          name: 'GitHub Actions',
          detail: 'Recreate on target instance with new credentials',
          context: { type: 'github_actions', enabled: 'true' },
        },
      ],
    },
    {
      // Rotated secrets have no layout of their own; they used to render as a
      // description with nothing listed under it.
      category: 'rotated_secret',
      title: 'Rotated Secrets',
      description: 'Recreating one needs its backing integration and credentials.',
      items: [
        {
          name: 'DB_PASSWORD',
          detail: 'Rotated secret in backend/prd via AWS Prod',
          context: { project: 'backend', config: 'prd' },
        },
      ],
    },
    {
      category: 'sync',
      title: 'Secrets Syncs',
      description: 'Secrets syncs cannot be listed via the Doppler API.',
      items: null,
    },
  ],
  user_check: {
    required: ['alice@example.com', 'bob@example.com'],
    existing: ['alice@example.com'],
    missing: ['bob@example.com'],
  },
};

describe('ManualStepsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders loading state while fetching', () => {
    (api.getManualSteps as Mock).mockReturnValue(new Promise(() => {}));
    renderPage();

    expect(screen.getByText('Manual Steps')).toBeInTheDocument();
    expect(screen.getByText(/Loading/)).toBeInTheDocument();
  });

  it('renders integration section with table', async () => {
    (api.getManualSteps as Mock).mockResolvedValue(fullResponse);
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Integrations')).toBeInTheDocument();
    });
    expect(screen.getByText('AWS SM')).toBeInTheDocument();
    expect(screen.getByText('GitHub Actions')).toBeInTheDocument();
    expect(screen.getByText(/aws_secrets_manager/)).toBeInTheDocument();
  });

  it('lists the items in a section', async () => {
    (api.getManualSteps as Mock).mockResolvedValue(fullResponse);
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Rotated Secrets')).toBeInTheDocument();
    });
    expect(screen.getByText('DB_PASSWORD')).toBeInTheDocument();
    expect(screen.getByText('Rotated secret in backend/prd via AWS Prod')).toBeInTheDocument();
  });

  it('renders secrets syncs section', async () => {
    (api.getManualSteps as Mock).mockResolvedValue(fullResponse);
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Secrets Syncs')).toBeInTheDocument();
    });
    expect(screen.getByText(/cannot be listed via the Doppler API/)).toBeInTheDocument();
  });

  it('renders missing users warning', async () => {
    (api.getManualSteps as Mock).mockResolvedValue(fullResponse);
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('bob@example.com')).toBeInTheDocument();
    });
    expect(screen.getByText(/Missing Users/)).toBeInTheDocument();
  });

  it('hides missing users section when none are missing', async () => {
    const noMissing: api.ManualStepsResponse = {
      ...fullResponse,
      user_check: { required: ['alice@example.com'], existing: ['alice@example.com'], missing: [] },
    };
    (api.getManualSteps as Mock).mockResolvedValue(noMissing);
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Integrations')).toBeInTheDocument();
    });
    expect(screen.queryByText(/Missing Users/)).not.toBeInTheDocument();
  });

  it('hides integration section when no integrations', async () => {
    const noInteg: api.ManualStepsResponse = {
      manual_steps: [
        {
          category: 'sync',
          title: 'Secrets Syncs',
          description: 'Syncs cannot be listed.',
          items: null,
        },
      ],
      user_check: { required: null, existing: null, missing: null },
    };
    (api.getManualSteps as Mock).mockResolvedValue(noInteg);
    renderPage();

    await waitFor(() => {
      expect(screen.getByText('Secrets Syncs')).toBeInTheDocument();
    });
    expect(screen.queryByText('Integrations')).not.toBeInTheDocument();
  });

  it('returns to the summary, having no step to continue to', async () => {
    // A checklist for after the run, reached from the summary — not a step
    // between Review and Migrate any more.
    const user = userEvent.setup();
    (api.getManualSteps as Mock).mockResolvedValue(fullResponse);
    renderPage();

    await user.click(await screen.findByRole('button', { name: /Back To Summary/ }));
    expect(mockedNavigate).toHaveBeenCalledWith('/complete');
    expect(screen.queryByRole('button', { name: /Continue/ })).not.toBeInTheDocument();
  });

  it('lists the items of a section that has no layout of its own', async () => {
    (api.getManualSteps as Mock).mockResolvedValue({
      manual_steps: [
        {
          category: 'webhook_secret',
          title: 'Webhook Signing Secrets',
          description: 'Signing secrets cannot be read back.',
          items: [{ name: 'https://hooks.example.com/deploy', detail: 'Webhook on backend' }],
        },
      ],
      user_check: { required: null, existing: null, missing: null },
    });
    renderPage();
    expect(await screen.findByText('https://hooks.example.com/deploy')).toBeInTheDocument();
  });

  it('says so when nothing needs doing by hand', async () => {
    (api.getManualSteps as Mock).mockResolvedValue({
      manual_steps: [],
      user_check: { required: null, existing: null, missing: null },
    });
    renderPage();
    expect(await screen.findByText('Nothing needs finishing by hand.')).toBeInTheDocument();
  });

  it('shows error and retry on fetch failure', async () => {
    const user = userEvent.setup();
    (api.getManualSteps as Mock)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValueOnce(fullResponse);

    renderPage();

    await waitFor(() => {
      expect(screen.getByText(/Network error/)).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Try Again' }));

    await waitFor(() => {
      expect(screen.getByText('Integrations')).toBeInTheDocument();
    });
    expect(api.getManualSteps).toHaveBeenCalledTimes(2);
  });

  it('renders all section descriptions', async () => {
    (api.getManualSteps as Mock).mockResolvedValue(fullResponse);
    renderPage();

    await waitFor(() => {
      expect(screen.getByText(/credentials are redacted/)).toBeInTheDocument();
    });
    expect(screen.getByText(/backing integration and credentials/)).toBeInTheDocument();
  });
});
