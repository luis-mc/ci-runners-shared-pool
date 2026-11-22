// The insight panes of the Job and Run pages (spec 4.5, 4.6): where the job ran, where its steps came from, CPU
// and memory, and the savings of a run.
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobDetail, jobsApi, runItem } from '../../testing/jobsData';
import { runsOnWords } from '../insights/JobInsights';

afterEach(cleanup);

describe('insight panes', () => {
  it('Job page names the runner, the steps source and the check', async () => {
    const detail = jobDetail({
      steps_source: 'rest',
      checked_at: new Date().toISOString(),
    });
    renderApp(jobsApi({ detail }), '/pool/jobs/1');
    expect(await screen.findByText(/crsp runner runner-1 on build-1/)).toBeTruthy();
    expect(screen.getByText(/Steps read from GitHub\./)).toBeTruthy();
    expect(screen.getByText(/Last checked with GitHub/)).toBeTruthy();
    expect(await screen.findByRole('region', { name: 'CPU and memory' })).toBeTruthy();
    expect(screen.getAllByText('2.5 cores').length).toBeGreaterThan(0);
  });

  it('says it in words for every origin', () => {
    const base = { runner_name: 'x', labels: ['ubuntu-latest'] };
    expect(runsOnWords({ ...base, runner_origin: 'hosted' })).toBe(
      'GitHub-hosted runner (ubuntu-latest)',
    );
    expect(runsOnWords(base)).toBe('not known yet');
    expect(
      runsOnWords({
        ...base,
        runner_origin: 'other',
        runner_group: 'g',
        runner_labels: ['a', 'b'],
      }),
    ).toBe('Another runner: x (group g; labels a, b)');
  });

  it('Run page shows the savings', async () => {
    renderApp(jobsApi({ run: runItem() }), '/pool/runs/100');
    expect(await screen.findByRole('region', { name: 'Savings' })).toBeTruthy();
    expect(screen.getByText(/The run would end 4 min sooner/)).toBeTruthy();
  });
});
