import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { vi, type Mock } from 'vitest';
import ConfigurationPage from '../../src/pages/ConfigurationPage';
import * as api from '../../src/api/client';

vi.mock('../../src/api/client', () => ({
  configure: vi.fn(),
  getConfiguration: vi.fn(),
}));

const mockedNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => mockedNavigate };
});

function renderPage() {
  return render(
    <MemoryRouter>
      <ConfigurationPage />
    </MemoryRouter>,
  );
}

const SA_TOKEN = 'dp.sa.0123456789abcdef7f2c';
const TARGET_TOKEN = 'dp.sa.fedcba98765432100b31';

/** Fills in a valid source, which is what unlocks the target. */
async function fillSource(user: ReturnType<typeof userEvent.setup>, token = SA_TOKEN) {
  const fields = screen.getAllByLabelText('API token');
  await user.type(fields[0], token);
}

/**
 * Fills in the target. It defaults to self-hosted — which is the whole point of
 * the tool — so it needs a host as well as a token.
 */
async function fillTarget(user: ReturnType<typeof userEvent.setup>) {
  const hosts = await screen.findAllByLabelText('Host');
  await user.type(hosts[hosts.length - 1], 'https://doppler.internal.acme.com');
  const fields = await screen.findAllByLabelText('API token');
  await user.type(fields[1], TARGET_TOKEN);
}

describe('ConfigurationPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getConfiguration as Mock).mockResolvedValue({
      configured: false,
      dev_mode: false,
      dev_mode_forced: false,
      has_source_token: false,
      has_target_token: false,
      migrate_workplace_name: false,
      stop_on_new_error_class: true,
    });
    (api.configure as Mock).mockResolvedValue({ valid: true, dev_mode: false });
  });

  it('renders the two endpoints and the options', async () => {
    renderPage();
    expect(screen.getByText('Configuration')).toBeInTheDocument();
    expect(screen.getByText('Source')).toBeInTheDocument();
    expect(screen.getByText('Target')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('Options')).toBeInTheDocument());
  });

  it('locks the target until the source is filled in, and says why in the card', async () => {
    // A disabled form whose explanation lives somewhere below it reads as a bug.
    renderPage();
    expect(await screen.findByText('Locked')).toBeInTheDocument();
    expect(
      screen.getByText(/Fill in the source first. The target unlocks/),
    ).toBeInTheDocument();
    expect(screen.getAllByLabelText('API token')).toHaveLength(1);
  });

  it('keeps the locked target the same height as the source', async () => {
    // The locked card holds one short message where the source holds a form, so
    // left to itself it sits visibly shorter than the card beside it. The row
    // stretches both and the lock panel grows to fill whatever height the source
    // sets — re-adding items-start to the grid is what would quietly undo it.
    renderPage();
    await screen.findByText('Locked');

    const panel = screen.getByText(/Fill in the source first/).closest('div');
    expect(panel).toHaveClass('flex-1');

    const card = panel?.parentElement;
    expect(card).toHaveClass('flex', 'flex-col');
    expect(card?.parentElement?.className).not.toContain('items-start');
  });

  it('unlocks the target once the source token is valid', async () => {
    const user = userEvent.setup();
    renderPage();
    await fillSource(user);

    await waitFor(() => expect(screen.getAllByLabelText('API token')).toHaveLength(2));
    expect(screen.queryByText('Locked')).not.toBeInTheDocument();
  });

  it('names the kind of token as it is typed', async () => {
    const user = userEvent.setup();
    renderPage();
    await fillSource(user, 'dp.ct.something');
    expect(await screen.findByText('CLI token')).toBeInTheDocument();
  });

  it('warns that a user-backed source token cannot read restricted secrets', async () => {
    // Saying this while the token is being typed is much cheaper than saying it
    // after two hundred configs have failed for want of it.
    const user = userEvent.setup();
    renderPage();
    await fillSource(user, 'dp.pt.something');

    expect(await screen.findByText('Personal token')).toBeInTheDocument();
    expect(screen.getByText(/cannot read secrets marked restricted/)).toBeInTheDocument();
  });

  it('does not warn about a service account token', async () => {
    const user = userEvent.setup();
    renderPage();
    await fillSource(user);

    expect(await screen.findByText('Service account token')).toBeInTheDocument();
    expect(screen.queryByText(/cannot read secrets marked restricted/)).not.toBeInTheDocument();
  });

  it('rejects a token that is not a Doppler token', async () => {
    const user = userEvent.setup();
    renderPage();
    await fillSource(user, 'not-a-token');
    expect(
      await screen.findByText('A Doppler token starts with dp.ct., dp.pt. or dp.sa.'),
    ).toBeInTheDocument();
  });

  it('keeps Continue disabled until both sides are ready', async () => {
    const user = userEvent.setup();
    renderPage();
    expect(screen.getByRole('button', { name: /Continue/ })).toBeDisabled();

    await fillSource(user);
    expect(screen.getByRole('button', { name: /Continue/ })).toBeDisabled();

    await fillTarget(user);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Continue/ })).not.toBeDisabled(),
    );
  });

  it('needs only a source in dev mode, because nothing is written', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByLabelText(/Dev Mode/));
    await fillSource(user);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Continue/ })).not.toBeDisabled(),
    );
    expect(screen.getByText(/Dev Mode is on, so nothing is written/)).toBeInTheDocument();
  });

  it('returns to the waiting page when sent from it to enter the tokens', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={[{ pathname: '/configure', state: { returnTo: '/standby' } }]}>
        <ConfigurationPage />
      </MemoryRouter>,
    );
    await fillSource(user);
    await fillTarget(user);
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    await waitFor(() => expect(mockedNavigate).toHaveBeenCalledWith('/standby'));
    expect(mockedNavigate).not.toHaveBeenCalledWith('/preflight');
  });

  it('submits and continues to pre-flight', async () => {
    const user = userEvent.setup();
    renderPage();
    await fillSource(user);
    await fillTarget(user);
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    await waitFor(() =>
      expect(api.configure).toHaveBeenCalledWith(
        expect.objectContaining({
          source_host: 'https://api.doppler.com',
          source_token: SA_TOKEN,
          target_token: TARGET_TOKEN,
        }),
      ),
    );
    expect(mockedNavigate).toHaveBeenCalledWith('/preflight');
  });

  it('sends stop-on-new-error-class on by default', async () => {
    // Without it a bad token burns through the whole workplace before anyone
    // looks at the screen.
    const user = userEvent.setup();
    renderPage();
    await fillSource(user);
    await fillTarget(user);
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    await waitFor(() =>
      expect(api.configure).toHaveBeenCalledWith(
        expect.objectContaining({ stop_on_new_error_class: true }),
      ),
    );
  });

  it('lets that be turned off', async () => {
    const user = userEvent.setup();
    renderPage();
    await fillSource(user);
    await fillTarget(user);
    await user.click(screen.getByLabelText(/Stop When A New Kind Of Error Appears/));
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    await waitFor(() =>
      expect(api.configure).toHaveBeenCalledWith(
        expect.objectContaining({ stop_on_new_error_class: false }),
      ),
    );
  });

  it('leaves the workplace rename off by default and sends it when asked', async () => {
    const user = userEvent.setup();
    renderPage();
    await fillSource(user);
    await fillTarget(user);
    await user.click(screen.getByLabelText(/Migrate The Workplace Name/));
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    await waitFor(() =>
      expect(api.configure).toHaveBeenCalledWith(
        expect.objectContaining({ migrate_workplace_name: true }),
      ),
    );
  });

  it('reports validation errors from the server', async () => {
    const user = userEvent.setup();
    (api.configure as Mock).mockResolvedValue({
      valid: false,
      errors: ['Target host must be a valid URL'],
      dev_mode: false,
    });

    renderPage();
    await fillSource(user);
    await fillTarget(user);
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    expect(await screen.findByText('Target host must be a valid URL')).toBeInTheDocument();
    expect(mockedNavigate).not.toHaveBeenCalled();
  });

  it('reports a network failure', async () => {
    const user = userEvent.setup();
    (api.configure as Mock).mockRejectedValue(new Error('Network error'));

    renderPage();
    await fillSource(user);
    await fillTarget(user);
    await user.click(screen.getByRole('button', { name: /Continue/ }));

    expect(await screen.findByText('Network error')).toBeInTheDocument();
  });

  it('asks for a host only when the instance is self-hosted', async () => {
    const user = userEvent.setup();
    renderPage();
    expect(screen.queryByLabelText('Host')).not.toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText('Instance'), 'onprem');
    expect(await screen.findByLabelText('Host')).toBeInTheDocument();
  });

  it('rejects a host that is not a URL', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.selectOptions(screen.getByLabelText('Instance'), 'onprem');
    await user.type(await screen.findByLabelText('Host'), 'not a url');
    expect(await screen.findByText('Must be a URL starting https://')).toBeInTheDocument();
  });

  it('restores a saved configuration and keeps the stored token', async () => {
    // Tokens never come back from the server, only a hint of which one is held.
    (api.getConfiguration as Mock).mockResolvedValue({
      configured: true,
      source_host: 'https://api.doppler.com',
      source_token_hint: 'dp.sa.…7f2c',
      has_source_token: true,
      target_host: 'https://doppler.internal.acme.com',
      target_token_hint: 'dp.sa.…b31a',
      has_target_token: true,
      dev_mode: false,
      dev_mode_forced: false,
      migrate_workplace_name: false,
      stop_on_new_error_class: true,
    });

    renderPage();
    expect(await screen.findByText('dp.sa.…7f2c')).toBeInTheDocument();
    const fields = screen.getAllByLabelText('API token') as HTMLInputElement[];
    expect(fields[0].value).toBe('');
  });

  it('omits an untouched token so the stored one is kept', async () => {
    const user = userEvent.setup();
    (api.getConfiguration as Mock).mockResolvedValue({
      configured: true,
      source_host: 'https://api.doppler.com',
      source_token_hint: 'dp.sa.…7f2c',
      has_source_token: true,
      target_host: 'https://doppler.internal.acme.com',
      target_token_hint: 'dp.sa.…b31a',
      has_target_token: true,
      dev_mode: false,
      dev_mode_forced: false,
      migrate_workplace_name: false,
      stop_on_new_error_class: true,
    });

    renderPage();
    await user.click(await screen.findByRole('button', { name: /Continue/ }));

    await waitFor(() => expect(api.configure).toHaveBeenCalled());
    const sent = (api.configure as Mock).mock.calls[0][0];
    expect(sent).not.toHaveProperty('source_token');
    expect(sent).not.toHaveProperty('target_token');
  });

  it('locks Dev Mode when the server forces it', async () => {
    (api.getConfiguration as Mock).mockResolvedValue({
      configured: false,
      dev_mode: true,
      dev_mode_forced: true,
      has_source_token: false,
      has_target_token: false,
      migrate_workplace_name: false,
      stop_on_new_error_class: true,
    });

    renderPage();
    expect(await screen.findByLabelText(/Dev Mode/)).toBeDisabled();
    expect(
      screen.getByText(/DEV_MODE is set in the server environment/),
    ).toBeInTheDocument();
  });

  it('goes back to the welcome screen', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(mockedNavigate).toHaveBeenCalledWith('/');
  });

  describe('tokens from the environment', () => {
    function startedWithTokens(sourceHint = 'dp.sa.…abcd') {
      (api.getConfiguration as Mock).mockResolvedValue({
        configured: false,
        has_source_token: true,
        source_token_hint: sourceHint,
        source_token_from_env: true,
        has_target_token: true,
        target_token_hint: 'dp.sa.…wxyz',
        target_token_from_env: true,
        dev_mode: false,
        dev_mode_forced: false,
        migrate_workplace_name: false,
        stop_on_new_error_class: true,
      });
    }

    it('fills both fields before anything is configured', async () => {
      startedWithTokens();
      renderPage();

      expect(await screen.findByText('SOURCE_DOPPLER_TOKEN')).toBeInTheDocument();
      // The source counts as filled, so the target unlocks without typing.
      expect(await screen.findByText('TARGET_DOPPLER_TOKEN')).toBeInTheDocument();
      expect(screen.queryByText('Locked')).not.toBeInTheDocument();
      // Only the hint is on screen; the fields themselves stay empty.
      for (const field of screen.getAllByLabelText('API token')) {
        expect(field).toHaveValue('');
        expect(field).toHaveAttribute('placeholder', expect.stringMatching(/^dp\.sa\.…/));
      }
    });

    it('sends no token, so the server keeps using the environment\'s', async () => {
      const user = userEvent.setup();
      startedWithTokens();
      (api.configure as Mock).mockResolvedValue({ valid: true, dev_mode: false });
      renderPage();

      await screen.findByText('TARGET_DOPPLER_TOKEN');
      await user.type(screen.getByLabelText('Host'), 'https://doppler.internal.example.com');
      await user.click(screen.getByRole('button', { name: /Continue/ }));

      await waitFor(() => expect(api.configure).toHaveBeenCalled());
      const sent = (api.configure as Mock).mock.calls[0][0];
      expect(sent).not.toHaveProperty('source_token');
      expect(sent).not.toHaveProperty('target_token');
    });

    it('still warns that a personal source token cannot read restricted secrets', async () => {
      // Nobody typed it, but the hint keeps the type prefix, and the warning is
      // what stops two hundred configs failing later.
      startedWithTokens('dp.pt.…abcd');
      renderPage();
      expect(await screen.findByText(/cannot read secrets marked restricted/)).toBeInTheDocument();
    });
  });

  it('links to how to create the source token from the Source card', async () => {
    renderPage();
    const link = await screen.findByRole('link', { name: 'How to create one' });
    expect(link).toHaveAttribute('href', '/permissions');
  });
});

