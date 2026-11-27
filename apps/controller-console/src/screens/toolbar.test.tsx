// CRSP132, web-console R5.67: every list has one shared toolbar row (search, filters, selection, count, actions).
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { MockApi, renderApp, repository, signedIn } from '../testing/mock';

afterEach(cleanup);

const repos = (api: MockApi): MockApi =>
  api.on('GET', '/api/v1/github/repositories', {
    items: [repository(), repository({ id: 8, full_name: 'octo/site', name: 'site' })],
    stale: { stale: false },
  });

describe('List toolbar (CRSP132)', () => {
  it('puts search, the Pool filter, selection words, count and Add in one toolbar', async () => {
    renderApp(repos(signedIn()), '/setup/pool');
    const bar = await screen.findByRole('group', { name: 'Repositories toolbar' });
    expect(
      within(bar).getByRole('textbox', { name: 'Search repositories' }),
    ).toBeTruthy();
    expect(within(bar).getByRole('group', { name: 'Pool' })).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Select all 2' })).toBeTruthy();
    expect(
      within(bar).getByRole<HTMLButtonElement>('button', { name: 'Clear' }).disabled,
    ).toBe(true);
    expect(within(bar).getByText('2 private repositories')).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Add a repository' })).toBeTruthy();
    fireEvent.click(within(bar).getByRole('button', { name: 'Select all 2' }));
    expect(
      within(screen.getByRole('region', { name: 'Actions' })).getByText('2 selected'),
    ).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Clear' })).toBeTruthy();
  });

  it('shows the two-state Pool filter as one joined choice with All', async () => {
    renderApp(repos(signedIn()), '/setup/pool');
    const group = await screen.findByRole('group', { name: 'Pool' });
    const names = within(group)
      .getAllByRole('button')
      .map((b) => b.textContent);
    expect(names).toEqual(['All', 'Pool on', 'Pool off']);
    expect(group.className).toBe('ds-segmented');
  });

  it('is on the machines list too', async () => {
    renderApp(signedIn(), '/setup/machines');
    expect(await screen.findByRole('group', { name: 'Machines toolbar' })).toBeTruthy();
  });
});
