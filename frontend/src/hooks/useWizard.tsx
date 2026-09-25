import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  WIZARD_STEPS,
  isSteplessRoute,
  stepIndexByPath,
  type WizardStepDef,
} from '@/components/wizard/steps';

interface WizardContextValue {
  steps: WizardStepDef[];
  currentStepIndex: number;
  furthestStepIndex: number;
  totalSteps: number;
  goToStep: (index: number) => void;
  /** True on routes that sit outside the wizard, like the welcome screen. */
  stepless: boolean;
  /** Marks the current step's circle as failed. Set by pre-flight and by the run. */
  hasBlockingError: boolean;
  setBlockingError: (value: boolean) => void;
}

const WizardContext = createContext<WizardContextValue | null>(null);

export function WizardProvider({ children }: { children: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();

  const stepless = isSteplessRoute(location.pathname);
  const currentStepIndex = stepIndexByPath(location.pathname);
  const [furthestStepIndex, setFurthestStepIndex] = useState(currentStepIndex);
  const [hasBlockingError, setBlockingError] = useState(false);

  useEffect(() => {
    setFurthestStepIndex((prev) => Math.max(prev, currentStepIndex));
  }, [currentStepIndex]);

  // A blocking error belongs to the step that raised it. Leaving it set as you
  // move on would put a failure marker on a step that never failed.
  useEffect(() => {
    setBlockingError(false);
  }, [currentStepIndex]);

  const goToStep = useCallback(
    (index: number) => {
      if (index >= 0 && index < WIZARD_STEPS.length && index <= furthestStepIndex) {
        navigate(WIZARD_STEPS[index].path);
      }
    },
    [furthestStepIndex, navigate],
  );

  return (
    <WizardContext.Provider
      value={{
        steps: WIZARD_STEPS,
        currentStepIndex,
        furthestStepIndex,
        totalSteps: WIZARD_STEPS.length,
        goToStep,
        stepless,
        hasBlockingError,
        setBlockingError,
      }}
    >
      {children}
    </WizardContext.Provider>
  );
}

export function useWizard(): WizardContextValue {
  const ctx = useContext(WizardContext);
  if (!ctx) {
    throw new Error('useWizard must be used within a WizardProvider');
  }
  return ctx;
}
