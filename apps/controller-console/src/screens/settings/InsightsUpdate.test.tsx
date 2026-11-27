// ci-run-insights R5.44 (owner decisions D18, D19): the owner chooses how often insights are re-analysed.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

describe('CI run insights update period', () => {
  it('shows every CI run by default and saves a slower period with the tab Save', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings', {})
      .on('PATCH', '/api/v1/settings', (c) => c.body);
    renderApp(a, '/settings/data');
    await screen.findByRole('heading', { name: 'CI run insights' });
    expect(
      (await screen.findByRole<HTMLInputElement>('radio', { name: /After every CI run/ }))
        .checked,
    ).toBe(true);
    expect(screen.queryByText('Runs between updates')).toBeNull();
    fireEvent.click(screen.getByRole('radio', { name: /Once an hour/ }));
    expect(await screen.findByText('Unsaved changes (1)')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('PATCH', '/api/v1/settings')).toBe(1));
    expect(a.calls.find((c) => c.method === 'PATCH')?.body).toMatchObject({
      insights_update_period: 'hourly',
    });
    expect(await screen.findByText('CI run insights saved.')).toBeTruthy();
  });

  it('asks for the number of runs when the period is every few runs', async () => {
    const a = signedIn().on('GET', '/api/v1/settings', {
      insights_update_period: 'every_nth_run',
      insights_update_every_n_runs: 8,
    });
    renderApp(a, '/settings/data');
    await screen.findByRole('heading', { name: 'CI run insights' });
    expect(await screen.findByText('Runs between updates')).toBeTruthy();
    expect(
      screen.getByRole<HTMLInputElement>('radio', { name: /After every few CI runs/ })
        .checked,
    ).toBe(true);
  });
});
