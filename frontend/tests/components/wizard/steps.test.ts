import {
  WIZARD_STEPS,
  isSteplessRoute,
  stepIndexByPath,
} from '@/components/wizard/steps';

describe('WIZARD_STEPS', () => {
  it('has seven steps', () => {
    // The token page and the manual steps list hang off the summary instead of
    // being steps: both are work for after the run, and neither can be done
    // before it.
    expect(WIZARD_STEPS).toHaveLength(7);
  });

  it('starts at Configure, not Welcome', () => {
    // Welcome sits before step 1: the run has not started, so there is nothing
    // for it to be a step of.
    expect(WIZARD_STEPS[0]).toMatchObject({ path: '/configure', label: 'Configure' });
  });

  it('ends at the summary', () => {
    expect(WIZARD_STEPS[WIZARD_STEPS.length - 1]).toMatchObject({
      path: '/complete',
      label: 'Summary',
    });
  });

  it('runs in dependency order', () => {
    expect(WIZARD_STEPS.map((s) => s.path)).toEqual([
      '/configure',
      '/preflight',
      '/gathering',
      '/scope',
      '/plan',
      '/migrate',
      '/complete',
    ]);
  });
});

describe('stepIndexByPath', () => {
  it.each([
    ['/configure', 0],
    ['/preflight', 1],
    ['/gathering', 2],
    ['/scope', 3],
    ['/plan', 4],
    ['/migrate', 5],
    ['/complete', 6],
  ])('maps %s to step %i', (path, index) => {
    expect(stepIndexByPath(path)).toBe(index);
  });

  it('keeps triage on the Migrate step', () => {
    // Triage is a view of the run, not a step of its own. Letting it fall
    // through to step 0 would send the stepper back to Configure mid-migration.
    expect(stepIndexByPath('/migrate/errors')).toBe(stepIndexByPath('/migrate'));
  });

  it('keeps the service token report on the Summary step', () => {
    expect(stepIndexByPath('/service-tokens')).toBe(stepIndexByPath('/complete'));
  });

  it('keeps the manual steps list on the Summary step', () => {
    // A checklist for after the run, reached from the summary.
    expect(stepIndexByPath('/manual-steps')).toBe(stepIndexByPath('/complete'));
  });

  it('falls back to the first step for an unknown path', () => {
    expect(stepIndexByPath('/nope')).toBe(0);
  });

  it('keeps the comparison inside the Scope step', () => {
    // The comparison is computed for the chosen scope, so it belongs to it.
    expect(stepIndexByPath('/compare')).toBe(stepIndexByPath('/scope'));
  });
});

describe('isSteplessRoute', () => {
  it('reports the welcome screen as outside the wizard', () => {
    expect(isSteplessRoute('/')).toBe(true);
  });

  it('reports wizard routes as inside it', () => {
    expect(isSteplessRoute('/configure')).toBe(false);
    expect(isSteplessRoute('/migrate')).toBe(false);
  });
});
