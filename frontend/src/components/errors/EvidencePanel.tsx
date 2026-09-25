import { ReactNode } from 'react';
import { Eyebrow } from '@/components/ui/primitives';
import type { Evidence } from '@/types/errors';

/**
 * The evidence panel: which token made the request, who it acts as, the exact
 * URL, what came back, and the body.
 *
 * This is the same component on the pre-flight screen and in the migration
 * error pane, deliberately. The remedy for a class of failure should be learned
 * once, and that only works if the failure looks the same in both places.
 *
 * Tokens arrive already reduced to a prefix and last four by the server. Nothing
 * here un-reduces them, and nothing here should ever be given a whole one.
 */

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[112px_1fr] items-baseline gap-3.5 border-b border-gray-75 px-3.5 py-2 text-[12.5px] last:border-b-0">
      <span className="text-gray-500">{label}</span>
      <div className="min-w-0">{children}</div>
    </div>
  );
}

function Mono({ children, bad = false }: { children: ReactNode; bad?: boolean }) {
  return (
    <span className={`font-mono break-words ${bad ? 'text-red-600' : 'text-gray-900'}`}>{children}</span>
  );
}

function Badge({ tone, children }: { tone: 'bad' | 'ok'; children: ReactNode }) {
  return (
    <span
      className={`ml-1.5 inline-flex h-[18px] items-center rounded-sm px-1.5 text-[11px] font-semibold ${
        tone === 'bad' ? 'bg-red-100 text-red-600' : 'bg-green-100 text-green-500'
      }`}
    >
      {children}
    </span>
  );
}

/** Token kinds that can read every secret visibility. */
const FULL_READ_KINDS = new Set(['service_account', 'service_account_identity']);

export default function EvidencePanel({
  evidence,
  title = 'Evidence',
  className = '',
}: {
  evidence: Evidence;
  title?: string;
  className?: string;
}) {
  const ok = evidence.status > 0 && evidence.status < 400;
  const tokenIsFullRead = evidence.token_kind ? FULL_READ_KINDS.has(evidence.token_kind) : false;

  // A panel with nothing in it is worse than none — it implies the tool looked
  // and found nothing, when in fact there was never an exchange to show.
  const hasAnything =
    evidence.token_hint || evidence.url || evidence.status || evidence.body;
  if (!hasAnything) return null;

  return (
    <div className={className}>
      {title && <Eyebrow className="mb-1.5">{title}</Eyebrow>}
      <div className="overflow-hidden rounded-md border border-gray-75 bg-gray-0">
        {evidence.token_hint && (
          <Row label="Token used">
            <Mono bad={!ok && !tokenIsFullRead}>{evidence.token_hint}</Mono>
            {evidence.token_kind_label && (
              <Badge tone={tokenIsFullRead ? 'ok' : 'bad'}>{evidence.token_kind_label}</Badge>
            )}
          </Row>
        )}

        {evidence.actor && (
          <Row label="Belongs to">
            <Mono>{evidence.actor}</Mono>
          </Row>
        )}

        {evidence.url && (
          <Row label={evidence.method === 'GET' ? 'Request' : 'Request'}>
            <Mono>
              {evidence.method} {evidence.url}
            </Mono>
          </Row>
        )}

        {evidence.status_text && (
          <Row label="Response">
            <Mono bad={!ok}>{evidence.status_text}</Mono>
            {evidence.latency_ms > 0 && (
              <span className="font-mono text-gray-500"> · {evidence.latency_ms} ms</span>
            )}
            {evidence.attempts > 1 && (
              <span className="font-mono text-gray-500"> · retried {evidence.attempts - 1}×</span>
            )}
            {evidence.retry_after > 0 && (
              <span className="font-mono text-gray-500"> · retry-after {evidence.retry_after}s</span>
            )}
          </Row>
        )}

        {evidence.body && (
          <Row label="Body">
            <pre className="mt-0.5 overflow-x-auto whitespace-pre rounded-sm border border-gray-75 bg-gray-50 px-3 py-2 font-mono text-[11.5px] leading-relaxed text-gray-700">
              {formatBody(evidence.body)}
            </pre>
          </Row>
        )}
      </div>
    </div>
  );
}

/**
 * Pretty-prints a JSON body so the messages array is readable, and leaves
 * anything else exactly as it arrived. A body that is not JSON is often the most
 * informative thing on the screen — an HTML error page from a proxy, say — so it
 * must not be swallowed when parsing fails.
 */
function formatBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}
