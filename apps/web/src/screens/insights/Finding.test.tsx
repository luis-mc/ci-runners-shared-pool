// Run insights, one finding (spec 4.1): the fix, a copyable snippet, evidence, the estimate; one primary only when
// the fix changes the crsp setup (UX-4, UX-8); Hide for 7 days is a secondary action (D17).
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';
import { stepsFor } from './steps';

afterEach(cleanup);

describe('Finding page', () => {
  it('offers Review the change for a setup fix', async () => {
    renderApp(jobsApi(), '/analytics/finding/1');
    expect(await screen.findByRole('button', { name: 'Review the change' })).toBeTruthy();
    expect(screen.getByText(/estimate/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Hide for 7 days' })).toBeTruthy();
  });

  it('hides the finding for 7 days and says when it comes back', async () => {
    renderApp(jobsApi(), '/analytics/finding/1');
    fireEvent.click(await screen.findByRole('button', { name: 'Hide for 7 days' }));
    expect(await screen.findByText(/Hidden until .* if it is still true/)).toBeTruthy();
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Hide for 7 days' }).disabled,
    ).toBe(true);
  });

  it('shows the snippet and no change primary for a repository fix', async () => {
    renderApp(jobsApi(), '/analytics/finding/2');
    await screen.findByLabelText('Workflow snippet');
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Review the change' })).toBeNull();
  });

  it('gives the numbered steps of a repository suggestion and names the repository', async () => {
    renderApp(jobsApi(), '/analytics/finding/2');
    const steps = await screen.findByRole('list', { name: 'Steps' });
    expect(steps.querySelectorAll('li').length).toBeGreaterThanOrEqual(3);
    expect(steps.textContent).toContain('octo/');
  });

  it('says so when the finding is not there', async () => {
    renderApp(jobsApi(), '/analytics/finding/99');
    expect(await screen.findByText(/Could not load this finding/)).toBeTruthy();
  });
});

describe('steps of a suggestion', () => {
  const kinds = [
    'wait.hold',
    'housekeeping.lane',
    'pool.size',
    'pool.idle_next_to_queue',
    'repo.slots',
    'run.critical_path',
    'run.superseded',
    'run.duplicate_event',
    'run.retested_tree',
    'run.fail_fast',
    'workflow.timeout',
    'workflow.retry',
    'workflow.matrix_axis',
    'workflow.step_order',
    'workflow.cache',
    'test.slow',
    'test.flaky',
    'test.duplicate',
    'test.no_assertions',
    'something.new',
  ];
  it.each(kinds)('%s has at least three steps', (kind) => {
    const steps = stepsFor({
      kind,
      repository: 'octo/app',
      scope: 'repo',
      subject: 'ci.yml',
    });
    expect(steps.length).toBeGreaterThanOrEqual(3);
  });
  it('names the repository in the steps of a repository suggestion', () => {
    for (const kind of kinds.filter(
      (k) => k !== 'pool.size' && k !== 'pool.idle_next_to_queue',
    ))
      expect(
        stepsFor({ kind, repository: 'octo/app', scope: 'repo' }).join(' '),
      ).toContain('octo/app');
  });
});
