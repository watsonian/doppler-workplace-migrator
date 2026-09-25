import { useEffect, useState } from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import { ArrowRight } from 'lucide-react';
import { WizardProvider, useWizard } from '@/hooks/useWizard';
import DopplerMark from '@/components/brand/DopplerMark';
import WizardStepper from './WizardStepper';
import RateLimitBanner from '@/components/RateLimitBanner';
import PageErrorBoundary from '@/components/PageErrorBoundary';
import FlashBanner from '@/components/FlashBanner';
import { getConfiguration } from '@/api/client';
import { RateDot, RateFigure, RatePanel, levelOf, useRateState, type SideState } from '@/components/RateIndicator';

export default function WizardLayout() {
  return (
    <WizardProvider>
      <Shell />
    </WizardProvider>
  );
}

function Shell() {
  const { stepless } = useWizard();
  const location = useLocation();

  return (
    <div className="flex min-h-screen flex-col bg-gray-25">
      <TopBar />
      {/* The welcome screen sits before step 1, so it gets no stepper — there is
          nothing yet for it to be a step of. */}
      {!stepless && (
        <div className="border-b border-gray-75 bg-gray-0">
          <div className="mx-auto max-w-[940px] px-6">
            <WizardStepper />
          </div>
        </div>
      )}
      <RateLimitBanner />
      <FlashBanner />
      <main className="flex-1">
        {/* Keyed by route, so a failure on one page does not stick to the next. */}
        <PageErrorBoundary key={location.pathname}>
          <Outlet />
        </PageErrorBoundary>
      </main>
    </div>
  );
}

/**
 * The top bar is the product's, not a hero: a mark, a name, and the endpoints in
 * mono. Whoever is running this needs to be able to glance up and confirm they
 * are pointed at the right two instances.
 */
function TopBar() {
  return (
    <header className="border-b border-gray-75 bg-gray-0">
      <div className="mx-auto flex max-w-[940px] items-center gap-2.5 px-6 py-3">
        <DopplerMark className="h-[22px] w-[22px]" />
        <span className="text-[13.5px] font-semibold text-gray-900">Workplace Migration</span>
        <Endpoints />
      </div>
    </header>
  );
}

/**
 * The configured source and target, asked of the server on each page change.
 * The hosts are what someone glances up to confirm, so they have to be right
 * however the session was configured — on the Configuration page, or resumed
 * from the environment after a restart, when that page never ran. It is one
 * small request to the local server per navigation.
 */
function Endpoints() {
  const { pathname } = useLocation();
  const [hosts, setHosts] = useState<{ source: string; target: string }>({ source: '', target: '' });

  useEffect(() => {
    let live = true;
    getConfiguration()
      .then((cfg) => {
        if (!live) return;
        setHosts(cfg.configured ? { source: cfg.source_host || '', target: cfg.target_host || '' } : { source: '', target: '' });
      })
      // Unknown is shown as the placeholder, which is what it is.
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [pathname]);

  const rates = useRateState();
  const [open, setOpen] = useState(false);
  const panelId = 'rate-limit-panel';

  // The endpoints double as the rate-limit indicator: a dot beside each host,
  // and the detail on hover, focus or click.
  return (
    <span
      className="relative ml-auto hidden sm:flex"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      <button
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        aria-label={`Rate limits: source ${levelWord(rates.source)}, target ${levelWord(rates.target)}`}
        onClick={() => setOpen((v) => !v)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        className="flex items-center gap-2 rounded-md px-1.5 py-1 text-[12.5px] text-gray-500 hover:bg-gray-25"
      >
        <span className="flex items-center gap-1.5">
          <RateDot state={rates.source} />
          <EndpointName kind="source" host={hosts.source} />
          <RateFigure state={rates.source} />
        </span>
        <ArrowRight size={12} className="text-gray-300" />
        <span className="flex items-center gap-1.5">
          <RateDot state={rates.target} />
          <EndpointName kind="target" host={hosts.target} />
          <RateFigure state={rates.target} />
        </span>
      </button>
      {open && (
        <div
          id={panelId}
          role="status"
          className="absolute right-0 top-full z-30 mt-1 rounded-md border border-gray-75 bg-gray-0 shadow-[0_8px_24px_rgba(19,20,22,0.12)]"
        >
          <RatePanel sides={rates} hosts={hosts} />
        </div>
      )}
    </span>
  );
}

function levelWord(state: SideState): string {
  const level = levelOf(state);
  return level === 'limited' ? 'rate limited' : level === 'warn' ? 'nearing a limit' : level === 'ok' ? 'fine' : 'no requests yet';
}

function EndpointName({ kind, host }: { kind: 'source' | 'target'; host: string }) {
  if (!host) return <span className="font-mono text-gray-300">{kind}</span>;
  return (
    <span className="font-mono text-gray-600" title={host}>
      {shortHost(host)}
    </span>
  );
}

/** Strips the scheme so the two endpoints fit side by side. */
function shortHost(host: string): string {
  return host.replace(/^https?:\/\//, '').replace(/\/$/, '');
}
