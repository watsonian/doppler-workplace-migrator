import { describe, expect, it } from 'vitest';
import { buildRows } from '@/pages/ComparePage';
import { describeContents } from '@/pages/PlanReviewPage';
import type { CompareResponse } from '@/api/client';

const newProject = {
  differences: [
    {
      kind: 'project',
      disposition: 'create',
      project: 'payments',
      name: 'payments',
      contains: { environment: 3, config: 3, secret: 145 },
    },
  ],
  matched: {},
} as unknown as CompareResponse;

describe('a project new to the target', () => {
  it('counts what it contains as additions, not as identical', () => {
    const rows = Object.fromEntries(buildRows(newProject).map((r) => [r.label, r]));
    expect(rows.Projects.create).toBe(1);
    expect(rows.Environments.create).toBe(3);
    expect(rows.Configs.create).toBe(3);
    expect(rows.Secrets.create).toBe(145);
    expect(rows.Webhooks.create).toBe(0);
  });

  it('describes its contents in the review', () => {
    expect(describeContents({ environment: 3, config: 1, secret: 1450 })).toBe(
      '3 environments, 1 config, 1,450 secrets',
    );
    expect(describeContents(undefined)).toBe('Empty');
  });
});
