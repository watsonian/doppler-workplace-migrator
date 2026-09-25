export interface WizardStepDef {
  path: string;
  label: string;
  /** Routes that belong to this step but are not the step itself. */
  alsoMatches?: string[];
}

/**
 * Seven steps, with full labels. Eight is the most a horizontal stepper holds at
 * the md breakpoint where it first appears, and only with its connectors
 * narrowed below lg (see WizardStepper).
 *
 * Welcome sits before step 1 rather than being one — the run has not started, so
 * there is nothing to be a step of. The service token page and the manual steps
 * list are reached from the summary rather than being steps: both are work for
 * after the run, and neither can be done before it. Manual used to sit between
 * Review and Migrate, asking to be read at a point where none of it could be
 * acted on, and listing the tokens the token page now handles.
 *
 * The waiting page belongs to Summary too: it is where a migration rests
 * between its full run and cutover, and every top-up ends back at it.
 *
 * Scope follows Gather because it needs the gathered project list, and comes
 * before the comparison because the comparison is computed for the scope.
 */
export const WIZARD_STEPS: WizardStepDef[] = [
  { path: '/configure', label: 'Configure' },
  { path: '/preflight', label: 'Pre-flight' },
  { path: '/gathering', label: 'Gather' },
  { path: '/scope', label: 'Scope', alsoMatches: ['/compare'] },
  { path: '/plan', label: 'Review' },
  { path: '/migrate', label: 'Migrate', alsoMatches: ['/migrate/errors'] },
  { path: '/complete', label: 'Summary', alsoMatches: ['/service-tokens', '/manual-steps', '/cutover', '/standby'] },
];

/** Routes that render outside the stepper entirely. */
export const STEPLESS_ROUTES = ['/', '/permissions'];

export function stepIndexByPath(pathname: string): number {
  const index = WIZARD_STEPS.findIndex(
    (s) => s.path === pathname || s.alsoMatches?.includes(pathname),
  );
  return index === -1 ? 0 : index;
}

export function isSteplessRoute(pathname: string): boolean {
  return STEPLESS_ROUTES.includes(pathname);
}
