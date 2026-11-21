// The insight panes of a Repository page (spec 4.4) and the Demand section of Size and backends (spec 4.3).
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, repository, signedIn } from '../../testing/mock';
import { jobDetail, jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('Repository insight panes', () => {
  it('shows CI checks, Guardrails and Job slots with a normal Review button', async () => {
    const r = repository({ id: 7, full_name: 'octo/app' });
    const api = signedIn()
      .on('GET', '/api/v1/github/repositories', { items: [r], stale: { stale: false } })
      .on('GET', '/api/v1/repositories/:id', r);
    renderApp(api, '/repositories/octo/app');
    expect(await screen.findByRole('region', { name: 'CI checks' })).toBeTruthy();
    expect(screen.getByText('The job has no timeout-minutes.')).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Guardrails' })).toBeTruthy();
    expect(await screen.findByRole('region', { name: 'Job slots' })).toBeTruthy();
    const review = await screen.findByRole('button', { name: 'Review the change' });
    expect(review.getAttribute('data-kind')).toBe('default');
  });
});

describe('Demand on Size and backends', () => {
  it('shows the demand and the estimates in words', async () => {
    renderApp(signedIn().on('GET', '/api/v1/machines', { items: [] }), '/pool/size');
    expect(await screen.findByRole('region', { name: 'Demand' })).toBeTruthy();
    expect(screen.getByText(/with 1 more slot the p95 wait would be about/)).toBeTruthy();
    expect(screen.getAllByText(/An estimate from the last 7 days/).length).toBe(2);
  });
});

describe('Measured peaks on the Job page', () => {
  const measured = (over: Record<string, unknown> = {}) => ({
    peak_cpu_cores: 3.46,
    peak_memory_bytes: 2_450_000_000,
    sample_count: 240,
    duration_seconds: 240,
    oom_killed: false,
    cut_short: false,
    good: true,
    key: {
      runs: 12,
      cpu_p90_cores: 2.6,
      memory_p90_bytes: 2_100_000_000,
      measured: true,
    },
    ...over,
  });
  const show = async (resources: Record<string, unknown>) => {
    const api = jobsApi({ detail: jobDetail() }).on(
      'GET',
      '/api/v1/jobs/:jobId/resources',
      resources,
    );
    renderApp(api, '/jobs/12');
    return within(await screen.findByRole('region', { name: 'CPU and memory' }));
  };
  const base = { samples: [] };

  it('shows the measured peaks with numbers and the typical peak of the job', async () => {
    const pane = await show({ ...base, measured: measured() });
    expect(pane.getByText('Measured peak CPU')).toBeTruthy();
    expect(pane.getByText('3.5 cores')).toBeTruthy();
    expect(pane.getByText('2.5 GB')).toBeTruthy();
    expect(pane.getByText('240')).toBeTruthy();
    expect(
      pane.getByText(
        'Typical peak of this job, p90 of the last 12 runs: 2.6 cores, 2.1 GB',
      ),
    ).toBeTruthy();
    expect(pane.queryByText(/not used for sizing/)).toBeNull();
  });

  it('says how far a job key is from measured, and when no run is good yet', async () => {
    const three = await show({
      ...base,
      measured: measured({ key: { runs: 3, measured: false } }),
    });
    expect(three.getByText('Not measured yet: 3 of 5 good runs.')).toBeTruthy();
    cleanup();
    const none = await show({
      ...base,
      measured: measured({ key: { runs: 0, measured: false } }),
    });
    expect(none.getByText('No good run yet.')).toBeTruthy();
  });

  it('flags a run killed for memory and a run cut short', async () => {
    const pane = await show({
      ...base,
      measured: measured({ oom_killed: true, cut_short: true, good: false }),
    });
    expect(
      pane.getByText(
        'This job was killed for memory. A run like this is not used for sizing.',
      ),
    ).toBeTruthy();
    expect(
      pane.getByText('This run was cut short. A run like this is not used for sizing.'),
    ).toBeTruthy();
  });

  it('keeps the old text when nothing was measured', async () => {
    const pane = await show({ ...base, unknown_reason: 'No agent reported this job.' });
    expect(pane.getByText('No agent reported this job.')).toBeTruthy();
    expect(pane.queryByText('Measured peak CPU')).toBeNull();
  });
});
