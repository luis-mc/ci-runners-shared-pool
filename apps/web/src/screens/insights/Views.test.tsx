// Run insights, Waits, Timeline, Tests and Guardrails (spec 4.1): each view reads its answer and keeps its h1.
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('the insight views', () => {
  it('Waits: names the range and lists repositories and jobs', async () => {
    renderApp(jobsApi(), '/jobs/insights/waits');
    await screen.findByRole('region', { name: 'By repository' });
    expect(
      screen.getByRole('button', { name: '7 days' }).getAttribute('aria-pressed'),
    ).toBe('true');
    expect(screen.getByRole('region', { name: 'By job' })).toBeTruthy();
    expect(screen.getByRole('heading', { level: 1, name: 'Jobs' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '30 days' }));
    expect(await screen.findByRole('region', { name: 'By repository' })).toBeTruthy();
  });

  it('Timeline: one lane for each runner, notes in words', async () => {
    renderApp(jobsApi(), '/jobs/insights/timeline');
    await screen.findByRole('region', { name: 'crsp-linuxbox-1' });
    expect(screen.getByRole('region', { name: 'old-pool-3' })).toBeTruthy();
    expect(screen.getByText('after-merge work')).toBeTruthy();
    expect(screen.getByText(/idle while a job waited/)).toBeTruthy();
  });

  it('Timeline: the runner wait of a follow-up run is a row of its own', async () => {
    renderApp(jobsApi(), '/jobs/insights/timeline');
    await screen.findByRole('region', { name: 'crsp-linuxbox-1' });
    expect(
      screen.getByText('Waited for a runner: octo/app installers.yml / msi'),
    ).toBeTruthy();
    expect(screen.getAllByText('follow-up run')).toHaveLength(2);
  });

  it('Tests: shows the limit in words and switches the view', async () => {
    renderApp(jobsApi(), '/jobs/insights/tests');
    expect((await screen.findAllByText(/over the unit limit of 10 s/)).length).toBe(2);
    fireEvent.click(screen.getByRole('button', { name: 'Flaky' }));
    expect(await screen.findByRole('region', { name: 'Flaky' })).toBeTruthy();
  });

  it('Guardrails: one cell for each guardrail with a word and a glyph', async () => {
    renderApp(jobsApi(), '/jobs/insights/guardrails');
    await screen.findByRole('region', { name: 'Guardrails' });
    expect(screen.getByText('✕ break 3')).toBeTruthy();
    expect(screen.getByText('◷ unknown')).toBeTruthy();
    expect(screen.getAllByText('✓ pass').length).toBe(5);
  });
});
