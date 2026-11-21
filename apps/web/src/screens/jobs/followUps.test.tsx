// CIX24 (ci-run-insights R5.47 to R5.49): a run is not finished while a run it started next is. The Run page and
// the Done list say "Started next: Installers, running", keep the run's own result, and the words come from words.ts.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi, runItem, runSummary } from '../../testing/jobsData';
import { followUpWords, startedNextWords } from './words';

afterEach(cleanup);

const installers = {
  workflow_name: 'Installers',
  run_id: 900,
  state: 'running' as const,
  started_at: '2026-11-21T06:57:34Z',
  link: 'https://github.com/octo/app/actions/runs/900',
};

describe('follow-up words', () => {
  it('says the workflow and where it stands', () => {
    expect(followUpWords(installers)).toBe('Installers, running');
    expect(followUpWords({ ...installers, state: 'done', result: 'failure' })).toBe(
      'Installers, failure',
    );
    expect(
      startedNextWords([
        installers,
        { ...installers, workflow_name: 'Docs', run_id: 901 },
      ]),
    ).toBe('Installers, running; Docs, running');
    expect(startedNextWords(undefined)).toBe('');
  });
});

describe('Run page, started next', () => {
  it('shows the follow-up next to the run result, with a link to its run', async () => {
    renderApp(
      jobsApi({
        run: runItem({
          overall_state: 'follow_up_running',
          started_next: [installers],
        }),
      }),
      '/jobs/run/100',
    );
    await screen.findByText('ci.yml on OPS34-jobs-section');
    expect(screen.getByText('follow-up running')).toBeTruthy();
    expect(screen.getAllByText('success').length).toBeGreaterThan(0); // the run's own result stays
    const link = screen.getByRole('link', { name: 'Installers' });
    expect(link.getAttribute('href')).toContain('/jobs/run/900');
    expect(link.parentElement?.textContent).toBe('Installers, running');
  });

  it('adds nothing for a run that started nothing', async () => {
    renderApp(jobsApi({ run: runItem() }), '/jobs/run/100');
    await screen.findByText('ci.yml on OPS34-jobs-section');
    expect(screen.queryByText('follow-up running')).toBeNull();
    expect(screen.queryByText('Started next')).toBeNull();
  });
});

describe('Done list, started next', () => {
  it('marks a finished run whose follow-up still runs, and names the follow-up', async () => {
    const runs = [
      runSummary({
        id: 100,
        overall_state: 'follow_up_running',
        started_next: [installers],
      }),
      runSummary({ id: 101, branch: 'other' }),
    ];
    renderApp(jobsApi({ runs }), '/jobs/done');
    const table = await screen.findByRole('table', { name: 'Done runs' });
    const rows = within(table).getAllByRole('row').slice(1);
    const withNext = rows.find((r) => r.textContent?.includes('#100'));
    const without = rows.find((r) => r.textContent?.includes('#101'));
    expect(withNext?.textContent).toContain('follow-up running');
    expect(withNext?.textContent).toContain('success');
    expect(without?.textContent).not.toContain('follow-up running');
  });
});
