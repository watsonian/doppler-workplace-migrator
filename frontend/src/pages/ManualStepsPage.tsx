import { useState, useEffect, useCallback } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowLeft, Loader2 } from 'lucide-react';
import {
  Block,
  Button,
  Callout,
  FooterBar,
  PageHeading,
} from '@/components/ui/primitives';
import { getManualSteps } from '@/api/client';
import type { ManualStepsResponse, ManualStep, UserCheckResult } from '@/api/client';

/** The sections still reach for a card and a note; these are the system's. */
const Card = ({ children, className = '' }: { children: React.ReactNode; className?: string }) => (
  <Block className={`p-4 ${className}`}>{children}</Block>
);
const Alert = ({
  variant,
  className = '',
  children,
}: {
  variant: 'info' | 'warning' | 'error' | 'success';
  className?: string;
  children: React.ReactNode;
}) => (
  <Callout tone={variant} className={className}>
    {children}
  </Callout>
);

function IntegrationSection({ step }: { step: ManualStep }) {
  return (
    <Card>
      <h2 className="mb-2 text-[15px] font-semibold text-gray-900">{step.title}</h2>
      <Alert variant="info" className="mb-4">
        <p>{step.description}</p>
      </Alert>
      {step.items && step.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-gray-200">
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Name</th>
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Type</th>
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Action Required</th>
              </tr>
            </thead>
            <tbody>
              {step.items.map((item) => (
                <tr key={item.name} className="border-b border-gray-100">
                  <td className="py-2 px-3 font-medium text-gray-800">{item.name}</td>
                  <td className="py-2 px-3">
                    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700">
                      {item.context?.type || 'unknown'}
                    </span>
                  </td>
                  <td className="py-2 px-3 text-gray-600">{item.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

/**
 * A section as a list of what it covers. Rotated secrets, webhook signing
 * secrets and personal configs have no layout of their own, and used to fall
 * through to a card with only the description — so the page said "these need
 * recreating" and then did not say which.
 */
function ItemListSection({ step }: { step: ManualStep }) {
  return (
    <Card>
      <h2 className="mb-2 text-[15px] font-semibold text-gray-900">{step.title}</h2>
      <Alert variant="info" className="mb-4">
        <p>
          {step.description}
          {step.category === 'unavailable' && (
            <>
              {' '}
              <Link to="/permissions" className="font-medium text-purple-500 hover:underline">
                See Source Token Permissions
              </Link>
            </>
          )}
        </p>
      </Alert>
      {step.items && step.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-gray-200">
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Name</th>
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Where</th>
              </tr>
            </thead>
            <tbody>
              {step.items.map((item, i) => (
                // Names repeat — the same rotated secret in two configs — so the
                // position keeps each row's key its own.
                <tr key={`${item.name}-${i}`} className="border-b border-gray-100">
                  <td className="py-2 px-3 font-medium text-gray-800">{item.name}</td>
                  <td className="py-2 px-3 text-gray-600">{item.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function SyncSection({ step }: { step: ManualStep }) {
  return (
    <Card>
      <h2 className="mb-2 text-[15px] font-semibold text-gray-900">{step.title}</h2>
      <Alert variant="info" className="mb-4">
        <p>{step.description}</p>
      </Alert>
      {step.items && step.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-gray-200">
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Description</th>
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Location</th>
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Integration</th>
                <th className="text-left py-2 px-3 text-gray-600 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {step.items.map((item) => (
                <tr key={item.name} className="border-b border-gray-100">
                  <td className="py-2 px-3 font-medium text-gray-800">{item.name}</td>
                  <td className="py-2 px-3 text-gray-600">
                    {item.context?.project}/{item.context?.config}
                  </td>
                  <td className="py-2 px-3 text-gray-600">{item.context?.integration}</td>
                  <td className="py-2 px-3">
                    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700">
                      {item.context?.status || 'unknown'}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function MissingUsersSection({ userCheck }: { userCheck: UserCheckResult }) {
  if (!userCheck.missing || userCheck.missing.length === 0) return null;

  return (
    <Card>
      <h2 className="mb-2 text-[15px] font-semibold text-gray-900">Missing Users</h2>
      <Alert variant="warning">
        <p className="mb-2">
          These users must be provisioned on the target instance (via SCIM or manual invite)
          before access control migration can complete. Migration will skip access assignments
          for missing users.
        </p>
        <ul className="list-disc list-inside space-y-0.5">
          {userCheck.missing.map((email) => (
            <li key={email} className="text-sm">{email}</li>
          ))}
        </ul>
      </Alert>
    </Card>
  );
}

function ManualStepSection({ step }: { step: ManualStep }) {
  switch (step.category) {
    case 'integration':
      return <IntegrationSection step={step} />;
    case 'sync':
      return <SyncSection step={step} />;
    default:
      return <ItemListSection step={step} />;
  }
}

export default function ManualStepsPage() {
  const navigate = useNavigate();
  const [data, setData] = useState<ManualStepsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchData = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await getManualSteps();
      setData(resp);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  return (
      <div className="mx-auto w-full max-w-[720px] space-y-3.5 px-6 py-6">
        {/* A checklist for after the run, reached from the summary. None of it can
            be done earlier — an integration or a sync needs the projects to exist
            on the target — and the one thing that does matter beforehand, users
            missing on the target, is raised on the Review page. Service tokens
            have a page of their own. */}
        <PageHeading
          title="Manual Steps"
          subtitle="What the API cannot carry across. Finish these on the target now that the migration has run."
        />

        {loading && (
          <div className="flex items-center justify-center gap-2.5 py-16 text-[13px] text-gray-500">
            <Loader2 size={16} className="animate-spin-slow text-purple-500" />
            Loading manual steps…
          </div>
        )}

        {error && (
          <Callout tone="error">
            <p className="mb-2">{error}</p>
            <Button variant="secondary" onClick={fetchData}>
              Try Again
            </Button>
          </Callout>
        )}

        {data && (
          <>
            {data.manual_steps?.map((step) => (
              <ManualStepSection key={step.category} step={step} />
            ))}

            {data.user_check && <MissingUsersSection userCheck={data.user_check} />}

            {(data.manual_steps?.length ?? 0) === 0 && !(data.user_check?.missing?.length) && (
              <Callout tone="success">Nothing needs finishing by hand.</Callout>
            )}
          </>
        )}

        <FooterBar
          left={
            <Button variant="ghost" onClick={() => navigate('/complete')}>
              <ArrowLeft size={13} /> Back To Summary
            </Button>
          }
        />
      </div>
  );
}
