import { render, screen } from '@testing-library/react';
import PhaseTimeline from '../../src/components/errors/PhaseTimeline';
import { mergePhases } from '../../src/pages/MigrationPage';

describe('PhaseTimeline', () => {
  it('sets a skipped phase apart from one that has not run', () => {
    render(
      <PhaseTimeline
        phases={[
          { key: 'a', name: 'Workplace & Roles', state: 'skipped', summary: 'Left for the full migration', classes: [] },
          { key: 'b', name: 'Webhooks', state: 'pending', classes: [] },
        ]}
      />,
    );
    expect(screen.getByText('Skipped')).toBeInTheDocument();
    expect(screen.getByText('Left for the full migration')).toBeInTheDocument();
    // Only the skipped one says so; a pending phase is faded, not labelled.
    expect(screen.getAllByText('Skipped')).toHaveLength(1);
  });
});

describe('mergePhases', () => {
  const blank = { current: 0, total: 0, lastItem: '' };

  it('fills in a phase the event stream missed', () => {
    const merged = mergePhases({}, {
      workplace_settings_and_roles: { status: 'skipped', reason: 'Left for the full migration' },
    });
    expect(merged.workplace_settings_and_roles).toMatchObject({
      status: 'skipped',
      reason: 'Left for the full migration',
    });
  });

  it('never moves a phase backwards', () => {
    const prev = { configs: { ...blank, status: 'completed' as const, current: 3, total: 3 } };
    expect(mergePhases(prev, { configs: { status: 'started' } })).toBe(prev);
  });

  it('keeps item progress when the server finishes a phase', () => {
    const prev = { configs: { ...blank, status: 'started' as const, current: 2, total: 3 } };
    expect(mergePhases(prev, { configs: { status: 'completed' } }).configs).toMatchObject({
      status: 'completed',
      current: 2,
      total: 3,
    });
  });
});
