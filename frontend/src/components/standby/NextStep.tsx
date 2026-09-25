import { Radio } from '@/components/ui/primitives';

export type NextStepChoice = 'wait' | 'cut_over';

/**
 * What happens after a full migration or a top-up: cut over now, or keep the
 * source in use and top up until then. Laid out like Full and Partial on the
 * Scope page, and with Wait selected by default — it can be undone and cutover
 * cannot, so the reflex click should be the safe one.
 */
export function NextStep({
  choice,
  onChoose,
  afterTopUp,
}: {
  choice: NextStepChoice;
  onChoose: (choice: NextStepChoice) => void;
  /** After a top-up, waiting is "keep waiting" rather than "start waiting". */
  afterTopUp?: boolean;
}) {
  return (
    <div className="flex flex-col gap-2.5">
      <div>
        <h2 className="text-sm font-semibold text-gray-900">What Next?</h2>
        <p className="mt-0.5 text-[12.5px] text-gray-500">
          Cutover applies the source’s trusted IP ranges to the target and is the last step.
        </p>
      </div>
      <div role="radiogroup" aria-label="What next" className="grid gap-3 md:grid-cols-2">
        <Option
          id="next-cut-over"
          checked={choice === 'cut_over'}
          onSelect={() => onChoose('cut_over')}
          title="Cut Over Now"
          description="The source has stopped changing and teams are ready to switch to the target."
        />
        <Option
          id="next-wait"
          checked={choice === 'wait'}
          onSelect={() => onChoose('wait')}
          title={afterTopUp ? 'Keep Waiting' : 'Wait, And Top Up Until Then'}
          description={
            afterTopUp
              ? 'Go back to the waiting page and top up again when the source changes.'
              : 'The source stays in use. Bring changes across as often as needed, and cut over when ready.'
          }
        />
      </div>
    </div>
  );
}

function Option({
  id,
  checked,
  onSelect,
  title,
  description,
}: {
  id: string;
  checked: boolean;
  onSelect: () => void;
  title: string;
  description: string;
}) {
  return (
    <label
      htmlFor={id}
      className={`flex cursor-pointer items-start gap-3 rounded-md border px-4 py-3.5 ${
        checked ? 'border-purple-500 bg-purple-50' : 'border-gray-75 bg-gray-0 hover:border-gray-100'
      }`}
    >
      <Radio id={id} name="next-step" checked={checked} onChange={onSelect} className="mt-0.5" />
      <span className="min-w-0">
        <b className="block text-sm font-semibold text-gray-900">{title}</b>
        <span className="mt-0.5 block text-[12.5px] leading-normal text-gray-600">{description}</span>
      </span>
    </label>
  );
}
