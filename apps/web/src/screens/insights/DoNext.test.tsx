// Analytics > Suggestions (spec 4.1, LOOK56): the first tab of Analytics; ranked by minutes, a tie to the older finding.
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('Do next', () => {
  it('opens Analytics on Suggestions, first of four tabs, and ranks by minutes saved', async () => {
    renderApp(jobsApi(), '/analytics');
    await screen.findByRole('region', { name: 'Do next' });
    const tabs = screen.getAllByRole('tab').map((t) => t.textContent);
    expect(tabs).toEqual(['Suggestions', 'Insights', 'Charts', 'Run elsewhere']);
    expect(
      screen.getByRole('tab', { name: 'Suggestions' }).getAttribute('aria-selected'),
    ).toBe('true');
    const rows = screen.getAllByRole('row').map((r) => r.textContent ?? '');
    const at = (t: string) => rows.findIndex((r) => r.includes(t));
    expect(at('wait a median')).toBeLessThan(at('no timeout'));
    expect(at('no timeout')).toBeLessThan(at('G1: 3 unit tests'));
    expect(screen.getAllByText('the pool').length).toBeGreaterThan(0);
  });

  it('keeps the repository filter and names the digest fold', async () => {
    renderApp(jobsApi(), '/analytics');
    await screen.findByRole('region', { name: 'Do next' });
    fireEvent.click(await screen.findByRole('button', { name: 'octo/api' }));
    expect(await screen.findByText(/3 unit tests run over/)).toBeTruthy();
    expect(screen.queryByText(/have no timeout/)).toBeNull();
    expect(screen.getByText(/Weekly digest, 2026-W46/)).toBeTruthy();
  });
});
