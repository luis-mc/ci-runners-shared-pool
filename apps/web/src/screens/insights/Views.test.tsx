// Run insights, Waits, Runner timeline, Tests and Guardrails (spec 4.1): each view reads its answer and keeps its h1.
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('the insight views', () => {
  it('Waits: a small table of the waits by job, beside the runner timeline', async () => {
    renderApp(jobsApi(), '/analytics/insights');
    const waits = await screen.findByRole('region', { name: 'Waits' });
    expect(
      (await within(waits).findAllByRole('columnheader')).map((h) => h.textContent),
    ).toEqual(['Label', 'Median', 'Slowest 5%']);
    expect(screen.getByRole('region', { name: 'Runner timeline' })).toBeTruthy();
    expect(screen.getByRole('heading', { level: 1, name: 'Analytics' })).toBeTruthy();
  });

  it('Runner timeline: one row for each runner, drawn by the shared timeline', async () => {
    renderApp(jobsApi(), '/analytics/insights');
    const pane = await screen.findByRole('region', { name: 'Runner timeline' });
    expect((await within(pane).findAllByText('crsp-linuxbox-1')).length).toBeGreaterThan(
      0,
    );
    expect(within(pane).getAllByText('old-pool-3').length).toBeGreaterThan(0);
    expect(pane.querySelectorAll('.ds-timeline__row').length).toBeGreaterThanOrEqual(2);
  });

  it('Tests: shows the limit in words and switches the view', async () => {
    renderApp(jobsApi(), '/analytics/tests');
    expect((await screen.findAllByText(/over the unit limit of 10 s/)).length).toBe(2);
    fireEvent.click(screen.getByRole('button', { name: 'Flaky' }));
    expect(await screen.findByRole('region', { name: 'Flaky' })).toBeTruthy();
  });

  it('Guardrails: one cell for each guardrail with a word and a glyph', async () => {
    renderApp(jobsApi(), '/analytics/guardrails');
    await screen.findByRole('region', { name: 'Guardrails' });
    expect(screen.getByText('✕ break 3')).toBeTruthy();
    expect(screen.getByText('◷ unknown')).toBeTruthy();
    expect(screen.getAllByText('✓ pass').length).toBe(5);
  });
});
