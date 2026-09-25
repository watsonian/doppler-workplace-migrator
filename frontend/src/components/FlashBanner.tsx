import { Info, X } from 'lucide-react';
import { useLocation, useNavigate } from 'react-router-dom';

/**
 * A one-time note carried by a redirect, saying why you are here and not where
 * you went. It lives in the navigation state, so it shows on the page the
 * redirect landed on and nowhere after: moving on, reloading, or dismissing it
 * all clear it.
 */
export default function FlashBanner() {
  const location = useLocation();
  const navigate = useNavigate();
  const flash = (location.state as { flash?: string } | null)?.flash;
  if (!flash) return null;

  function dismiss() {
    navigate(`${location.pathname}${location.search}`, { replace: true, state: null });
  }

  return (
    <div className="mx-auto w-full max-w-[940px] px-6 pt-4">
      <div
        role="status"
        className="flex items-start gap-2.5 rounded-md border border-blue-100 bg-blue-50 px-3.5 py-2.5 text-[13px] text-gray-800"
      >
        <Info size={15} aria-hidden className="mt-0.5 shrink-0 text-blue-500" />
        <span className="min-w-0 flex-1">{flash}</span>
        <button
          type="button"
          onClick={dismiss}
          aria-label="Dismiss"
          className="-mr-1 grid h-6 w-6 shrink-0 place-items-center rounded text-gray-500 hover:bg-blue-100 hover:text-gray-800"
        >
          <X size={14} />
        </button>
      </div>
    </div>
  );
}
