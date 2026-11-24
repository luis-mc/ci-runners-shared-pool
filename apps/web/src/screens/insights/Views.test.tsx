// Run insights, Tests and Guardrails (spec 4.1): each view reads its answer and keeps its h1.
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('the insight views', () => {
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
