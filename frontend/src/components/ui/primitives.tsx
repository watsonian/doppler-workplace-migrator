import {
  ButtonHTMLAttributes,
  forwardRef,
  HTMLAttributes,
  ReactNode,
  useEffect,
  useId,
  useRef,
} from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  Circle,
  Info,
  Loader2,
  MinusCircle,
  XCircle,
} from 'lucide-react';

/* ── Buttons ────────────────────────────────────────────────────────────────
 * Title Case labels, per the design system. Labels never carry counts: a button
 * that says "Re-run 214 Configs" is wrong the moment the run writes one more.
 */

type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
type ButtonSize = 'sm' | 'md';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

const buttonVariants: Record<ButtonVariant, string> = {
  primary:
    'bg-purple-500 text-white border-transparent hover:bg-purple-600 active:bg-purple-700 disabled:bg-gray-50 disabled:text-gray-300 disabled:border-gray-75',
  secondary:
    'bg-gray-0 text-gray-800 border-gray-100 hover:bg-gray-25 hover:border-gray-200 disabled:bg-gray-50 disabled:text-gray-300',
  ghost:
    'bg-transparent text-gray-600 border-transparent hover:bg-gray-50 hover:text-gray-800 disabled:text-gray-300',
  danger:
    'bg-red-500 text-white border-transparent hover:bg-red-600 disabled:bg-gray-50 disabled:text-gray-300',
};

const buttonSizes: Record<ButtonSize, string> = {
  sm: 'h-8 px-3 text-[13px]',
  md: 'h-10 px-4 text-sm',
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = 'primary', size = 'sm', className = '', children, ...props }, ref) => (
    <button
      ref={ref}
      className={`inline-flex items-center justify-center gap-2 rounded-md border font-semibold whitespace-nowrap transition-colors disabled:cursor-not-allowed ${buttonVariants[variant]} ${buttonSizes[size]} ${className}`}
      {...props}
    >
      {children}
    </button>
  ),
);
Button.displayName = 'Button';

/* ── Pills ──────────────────────────────────────────────────────────────── */

export type PillTone = 'done' | 'running' | 'waiting' | 'skipped' | 'warning' | 'error';

const pillTones: Record<PillTone, string> = {
  done: 'bg-green-100 text-green-500',
  running: 'bg-purple-100 text-purple-500',
  waiting: 'bg-gray-50 text-gray-500',
  skipped: 'bg-gray-50 text-gray-400',
  warning: 'bg-yellow-100 text-yellow-500',
  error: 'bg-red-100 text-red-600',
};

export function Pill({
  tone,
  children,
  className = '',
}: {
  tone: PillTone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 h-[22px] px-2 rounded-md text-xs font-medium whitespace-nowrap ${pillTones[tone]} ${className}`}
    >
      {children}
    </span>
  );
}

/* ── Block: the product's bordered container ───────────────────────────── */

export function Block({ className = '', children, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={`border border-gray-75 rounded-md bg-gray-0 overflow-hidden ${className}`}
      {...props}
    >
      {children}
    </div>
  );
}

export function BlockHeader({
  title,
  children,
}: {
  title: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="bg-gray-50 border-b border-gray-75 px-3.5 py-2.5 flex items-center justify-between gap-3">
      <h2 className="text-[13.5px] font-semibold text-gray-900">{title}</h2>
      {children}
    </div>
  );
}

export function BlockRow({
  className = '',
  muted = false,
  children,
  ...props
}: HTMLAttributes<HTMLDivElement> & { muted?: boolean }) {
  return (
    <div
      className={`flex items-center gap-2.5 px-3.5 py-2 text-[13px] border-b border-gray-75 last:border-b-0 ${
        muted ? 'opacity-50' : ''
      } ${className}`}
      {...props}
    >
      {children}
    </div>
  );
}

/* ── Progress ───────────────────────────────────────────────────────────── */

export function ProgressBar({
  value,
  className = '',
  label,
}: {
  /** 0–100. */
  value: number;
  className?: string;
  label?: string;
}) {
  const pct = Math.max(0, Math.min(100, Math.round(value)));
  return (
    <div
      className={`h-1.5 rounded-full bg-gray-75 overflow-hidden ${className}`}
      role="progressbar"
      aria-valuenow={pct}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label={label}
    >
      <div className="h-full rounded-full bg-purple-500 transition-[width]" style={{ width: `${pct}%` }} />
    </div>
  );
}

/* ── Status icons ───────────────────────────────────────────────────────── */

export type ItemStatus =
  | 'passed'
  | 'completed'
  | 'failed'
  | 'warning'
  | 'running'
  | 'skipped'
  | 'pending';

export function StatusIcon({
  status,
  size = 15,
  className = '',
}: {
  status: ItemStatus;
  size?: number;
  className?: string;
}) {
  switch (status) {
    case 'passed':
    case 'completed':
      return <CheckCircle2 size={size} className={`text-green-500 shrink-0 ${className}`} />;
    case 'failed':
      return <AlertTriangle size={size} className={`text-red-500 shrink-0 ${className}`} />;
    case 'warning':
      return <AlertTriangle size={size} className={`text-yellow-500 shrink-0 ${className}`} />;
    case 'running':
      return (
        <Loader2 size={size} className={`text-purple-500 shrink-0 animate-spin-slow ${className}`} />
      );
    case 'skipped':
      return <MinusCircle size={size} className={`text-gray-400 shrink-0 ${className}`} />;
    default:
      return <Circle size={size} className={`text-gray-300 shrink-0 ${className}`} />;
  }
}

/* ── Callouts ───────────────────────────────────────────────────────────── */

type CalloutTone = 'info' | 'warning' | 'error' | 'success';

const calloutTones: Record<CalloutTone, { box: string; icon: string; Icon: typeof Info }> = {
  info: { box: 'bg-blue-50 border-blue-100', icon: 'text-blue-500', Icon: Info },
  warning: { box: 'bg-yellow-50 border-yellow-100', icon: 'text-yellow-500', Icon: AlertTriangle },
  error: { box: 'bg-red-50 border-red-100', icon: 'text-red-500', Icon: XCircle },
  success: { box: 'bg-green-50 border-green-100', icon: 'text-green-500', Icon: CheckCircle2 },
};

export function Callout({
  tone,
  children,
  className = '',
}: {
  tone: CalloutTone;
  children: ReactNode;
  className?: string;
}) {
  const { box, icon, Icon } = calloutTones[tone];
  return (
    <div
      role="alert"
      className={`flex gap-2.5 items-start border rounded-md px-3.5 py-3 text-[13px] text-gray-700 ${box} ${className}`}
    >
      <Icon size={16} className={`${icon} shrink-0 mt-0.5`} />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

/* ── Field ──────────────────────────────────────────────────────────────── */

/*
 * Field furniture, from `.dp-field-label` / `.dp-field-hint`: both 12px, the
 * label at weight 400 on gray-975 and the hint on gray-600.
 */
export function Label({ htmlFor, children }: { htmlFor: string; children: ReactNode }) {
  return (
    <label htmlFor={htmlFor} className="block text-xs font-normal leading-normal text-gray-975">
      {children}
    </label>
  );
}

export function Hint({ tone = 'muted', children }: { tone?: 'muted' | 'good' | 'bad'; children: ReactNode }) {
  const tones = { muted: 'text-gray-600', good: 'text-green-500', bad: 'text-red-600' };
  return <p className={`text-xs leading-normal ${tones[tone]}`}>{children}</p>;
}

/*
 * TextField and Select share one set of values, taken from the product's
 * `.dp-input-group` (server/src/index.css, the `--bg-*` / `--border-*` custom
 * properties) rather than from anything approximated here:
 *
 *   height     30px inner + 1px borders = 32px (sm) · 38 + 2 = 40px (md)
 *   background gray-25, hover gray-50       (the `inset` variant is gray-0)
 *   border     gray-75, hover gray-100
 *   focus      purple-500 border by pointer, blue outline by keyboard
 *   invalid    red-600 · warning yellow-600
 *   disabled   bg gray-75, border gray-100, text gray-600
 *   type       14px sans, gray-975, placeholder gray-500
 *
 * The two focus signals are never shown together. The product defines them as
 * mutually exclusive — `[data-focus]:not([data-focus-visible])` for the purple
 * border, `[data-focus-visible]` for the blue outline — so clicking a field
 * gives you purple only and tabbing to it gives you blue only. Purple says
 * "this is the field you are in"; blue says "this is where the keyboard is",
 * which you do not need telling when you just clicked. Both live in
 * styles/index.css.
 */

type FieldSize = 'sm' | 'md';

const fieldSizes: Record<FieldSize, string> = {
  sm: 'h-8',
  md: 'h-10',
};

/**
 * Everything a field and a select hold in common.
 *
 * `dp-control` is the hook for the pointer-focus border. The purple one cannot
 * live here as a `focus:` utility: it has to fire only when a pointer put you
 * in the field and never when the keyboard did, and that depends on the last
 * interaction rather than on the element. styles/index.css holds both halves
 * together so the pair stays legible; lib/focusModality.ts explains why native
 * `:focus-visible` cannot express it.
 */
const fieldBase =
  'dp-control w-full rounded-md border px-3 text-sm leading-normal text-gray-975 transition-colors ' +
  'disabled:border-gray-100 disabled:bg-gray-75 disabled:text-gray-600 disabled:cursor-not-allowed';

/** The resting and hover border and background, which invalid and valid override. */
function fieldState(invalid?: boolean, valid?: boolean, inset?: boolean): string {
  const bg = inset ? 'bg-gray-0 hover:bg-gray-25' : 'bg-gray-25 hover:bg-gray-50';
  if (invalid) return `${bg} border-red-600`;
  if (valid) return `${bg} border-green-500`;
  return `${bg} border-gray-75 hover:border-gray-100`;
}

interface TextFieldProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, 'size'> {
  label?: string;
  hint?: ReactNode;
  hintTone?: 'muted' | 'good' | 'bad';
  invalid?: boolean;
  valid?: boolean;
  size?: FieldSize;
  /** Sits on a gray surface rather than a white one. */
  inset?: boolean;
  /**
   * Renders in mono. Off by default, because the product's inputs are sans —
   * but the system reserves mono for identifiers, and a host, a token or a CIDR
   * is one, so the fields holding those ask for it.
   */
  mono?: boolean;
}

export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(
  (
    {
      label,
      hint,
      hintTone = 'muted',
      invalid,
      valid,
      size = 'md',
      inset,
      mono = false,
      className = '',
      id,
      ...props
    },
    ref,
  ) => {
    const inputId = id ?? props.name ?? undefined;
    return (
      <div className="flex flex-col gap-1">
        {label && inputId && <Label htmlFor={inputId}>{label}</Label>}
        <input
          ref={ref}
          id={inputId}
          className={`${fieldBase} ${fieldSizes[size]} ${fieldState(invalid, valid, inset)} placeholder:text-gray-500 ${
            mono ? 'font-mono' : ''
          } ${className}`}
          {...props}
        />
        {hint && <Hint tone={hintTone}>{hint}</Hint>}
      </div>
    );
  },
);
TextField.displayName = 'TextField';

/* ── Misc ───────────────────────────────────────────────────────────────── */

/** A small uppercase section label. */
export function Eyebrow({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <div className={`text-[10.5px] font-semibold uppercase tracking-[0.06em] text-gray-400 ${className}`}>
      {children}
    </div>
  );
}

/** The centered page heading used on every wizard step. */
export function PageHeading({ title, subtitle }: { title: string; subtitle?: ReactNode }) {
  return (
    <div className="text-center">
      <h1 className="text-[22px] font-semibold tracking-[-0.015em] text-gray-900">{title}</h1>
      {subtitle && <p className="mt-1.5 text-[13px] text-gray-500">{subtitle}</p>}
    </div>
  );
}

/** The footer bar every step ends with: back on the left, continue on the right. */
export function FooterBar({ left, right }: { left?: ReactNode; right?: ReactNode }) {
  return (
    <div className="mt-4 flex items-center justify-between gap-4 border-t border-gray-75 pt-3.5">
      <div className="flex items-center gap-2 min-w-0">{left}</div>
      <div className="flex items-center gap-2">{right}</div>
    </div>
  );
}

/**
 * The platform checkbox, tinted.
 *
 * This was a styled span with a text tick in it, which is how it ended up
 * looking like a radio button: the design system sets a default border radius of
 * 8px, and on a 15px box that is a circle. A real input cannot drift that way,
 * and brings its own keyboard and assistive-technology behaviour rather than
 * needing a hidden twin to supply it.
 */
/* ── Modal ──────────────────────────────────────────────────────────────────
 * Used where a decision has to be taken before anything else happens, which in
 * this tool means the once-only token value and the confirmations around it.
 *
 * `dismissible` is off for the reveal: that dialog holds the only copy of
 * something unrecoverable, so backdrop clicks and Escape must not discard it by
 * reflex. Everything else closes the way a dialog normally does.
 */
export function Modal({
  title,
  subtitle,
  icon,
  tone = 'neutral',
  width = 560,
  dismissible = true,
  onClose,
  footer,
  children,
}: {
  title: string;
  subtitle?: ReactNode;
  icon?: ReactNode;
  tone?: 'neutral' | 'warning' | 'error' | 'success';
  width?: number;
  dismissible?: boolean;
  onClose: () => void;
  footer?: ReactNode;
  children?: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const titleId = useId();

  useEffect(() => {
    // Move focus in, so the keyboard lands on the decision rather than behind it.
    ref.current?.focus();
  }, []);

  useEffect(() => {
    if (!dismissible) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [dismissible, onClose]);

  const iconTones = {
    neutral: 'bg-gray-50 text-gray-500',
    warning: 'bg-yellow-100 text-yellow-500',
    error: 'bg-red-100 text-red-600',
    success: 'bg-green-100 text-green-500',
  } as const;

  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-gray-950/40 p-6"
      onClick={dismissible ? onClose : undefined}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        style={{ width }}
        className="max-h-full w-full max-w-full overflow-y-auto rounded-lg bg-gray-0 shadow-[0_14px_44px_rgba(19,20,22,0.28)] outline-none"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start gap-3 px-5 pt-5">
          {icon && (
            <span className={`grid h-8 w-8 shrink-0 place-items-center rounded-md ${iconTones[tone]}`}>
              {icon}
            </span>
          )}
          <div className="min-w-0">
            <h3 id={titleId} className="text-[16.5px] font-semibold tracking-[-0.01em] text-gray-900">
              {title}
            </h3>
            {subtitle && <p className="mt-0.5 text-[12.5px] text-gray-500">{subtitle}</p>}
          </div>
        </div>

        <div className="flex flex-col gap-3 px-5 pb-5 pt-4">{children}</div>

        {footer && (
          <div className="flex items-center justify-end gap-2 border-t border-gray-75 bg-gray-25 px-5 py-3">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}

/*
 * Checkbox and Radio are native controls with the accent set, rather than boxes
 * drawn out of divs: the platform already gets the indeterminate state, the
 * keyboard behaviour and the hit area right, and a hand-drawn one has to
 * re-earn all three. The accent is the whole of the styling, and it is the
 * point — left to the browser these render in the OS accent, which on most
 * machines is blue, and blue means focus everywhere else in this product.
 */
export const Checkbox = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className = '', ...props }, ref) => (
    <input
      ref={ref}
      type="checkbox"
      className={`h-[15px] w-[15px] shrink-0 cursor-pointer accent-purple-500 disabled:cursor-not-allowed ${className}`}
      {...props}
    />
  ),
);
Checkbox.displayName = 'Checkbox';

export const Radio = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className = '', ...props }, ref) => (
    <input
      ref={ref}
      type="radio"
      className={`h-[15px] w-[15px] shrink-0 cursor-pointer accent-purple-500 disabled:cursor-not-allowed ${className}`}
      {...props}
    />
  ),
);
Radio.displayName = 'Radio';

/* ── Select ─────────────────────────────────────────────────────────────────
 * `.dp-select` is the same input-group chrome at md, plus a gray-300 caret and
 * a purple border while open.
 *
 * This stays a native <select>: the product draws a custom popover, but that
 * buys a rewrite of keyboard navigation, typeahead and screen-reader semantics
 * that the platform already gets right, for one picker. `appearance-none` drops
 * the OS control so the caret can be the system's; the menu the OS opens is the
 * one visible difference, and it is not one this app can style anyway.
 */
interface SelectProps extends Omit<React.SelectHTMLAttributes<HTMLSelectElement>, 'size'> {
  label?: string;
  hint?: ReactNode;
  size?: FieldSize;
  inset?: boolean;
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(
  ({ label, hint, size = 'md', inset, className = '', id, children, ...props }, ref) => {
    const selectId = id ?? props.name ?? undefined;
    return (
      <div className="flex flex-col gap-1">
        {label && selectId && <Label htmlFor={selectId}>{label}</Label>}
        <div className="relative">
          <select
            ref={ref}
            id={selectId}
            className={`${fieldBase} ${fieldSizes[size]} ${fieldState(
              false,
              false,
              inset,
            )} cursor-pointer appearance-none pr-9 ${className}`}
            {...props}
          >
            {children}
          </select>
          <ChevronDown
            size={12}
            aria-hidden
            className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-gray-300"
          />
        </div>
        {hint && <Hint>{hint}</Hint>}
      </div>
    );
  },
);
Select.displayName = 'Select';
