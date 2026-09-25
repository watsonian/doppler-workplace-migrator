import { Check } from 'lucide-react';
import { useWizard } from '@/hooks/useWizard';

/**
 * The horizontal stepper.
 *
 * Completed steps are clickable so you can look back at what pre-flight said;
 * steps ahead are not, because the migrator is a linear job and a stepper that
 * lets you jump forward is offering something the tool cannot honour.
 */
export default function WizardStepper() {
  const { steps, currentStepIndex, furthestStepIndex, totalSteps, goToStep, hasBlockingError } =
    useWizard();

  return (
    <>
      {/* Narrow screens get the position as a sentence — seven labelled circles
          do not fit, and an unreadable stepper is worse than none. */}
      <div className="py-3 text-center md:hidden">
        <span className="text-[13px] font-medium text-gray-600">
          Step {currentStepIndex + 1} of {totalSteps} — {steps[currentStepIndex].label}
        </span>
      </div>

      <nav
        aria-label="Wizard progress"
        className="hidden items-center justify-center py-4 md:flex"
      >
        {steps.map((step, index) => {
          const isCompleted = index < currentStepIndex;
          const isCurrent = index === currentStepIndex;
          const isClickable = index < currentStepIndex && index <= furthestStepIndex;
          const showsError = isCurrent && hasBlockingError;

          return (
            <div key={step.path} className="flex items-center">
              {index > 0 && (
                // Narrower below lg: eight full labels then fit from md, where the
                // stepper first appears, instead of overflowing it.
                <span aria-hidden className="mx-1.5 h-px w-2.5 bg-gray-100 lg:mx-2 lg:w-[18px]" />
              )}

              <button
                type="button"
                onClick={() => isClickable && goToStep(index)}
                disabled={!isClickable}
                aria-current={isCurrent ? 'step' : undefined}
                aria-label={`Step ${index + 1}: ${step.label}`}
                className={`flex items-center gap-2 text-xs transition-colors ${
                  isCurrent
                    ? 'font-semibold text-gray-900'
                    : isCompleted
                      ? 'text-gray-600'
                      : 'text-gray-400'
                } ${isClickable ? 'cursor-pointer hover:text-gray-900' : 'cursor-default'}`}
              >
                <span
                  aria-hidden
                  className={`grid h-5 w-5 place-items-center rounded-full border text-[10px] font-semibold ${
                    showsError
                      ? 'border-red-500 bg-red-500 text-white'
                      : isCompleted
                        ? 'border-green-500 bg-green-500 text-white'
                        : isCurrent
                          ? 'border-purple-500 bg-purple-500 text-white'
                          : 'border-gray-100 text-gray-400'
                  }`}
                >
                  {showsError ? '!' : isCompleted ? <Check size={11} strokeWidth={3} /> : index + 1}
                </span>
                {step.label}
              </button>
            </div>
          );
        })}
      </nav>
    </>
  );
}
