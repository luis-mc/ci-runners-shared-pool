// CRSP132, web-console R5.67: every list has one shared toolbar row (search, filters, selection, count, actions).
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { MockApi, renderApp, repository, signedIn } from '../testing/mock';

afterEach(cleanup);

const repos = (api: MockApi): MockApi =>
  api.on('GET', '/api/v1/github/repositories', {
    items: [repository(), repository({ id: 8, full_name: 'octo/site', name: 'site' })],
    stale: { stale: false },
  });

describe('List toolbar (CRSP132)', () => {
  it('puts search, the types, the count and Add in one toolbar', async () => {
    renderApp(repos(signedIn()), '/setup/pool');
    const bar = await screen.findByRole('group', { name: 'Repositories toolbar' });
    expect(
      within(bar).getByRole('textbox', { name: 'Search repositories' }),
    ).toBeTruthy();
    expect(within(bar).getByText('2 private repositories')).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Add a repository' })).toBeTruthy();
    // The groups below carry the pool state; the toolbar has no Pool filter (CRSP340).
    expect(within(bar).queryByRole('group', { name: 'Pool' })).toBeNull();
  });

  it('is on the machines list too', async () => {
    renderApp(signedIn(), '/setup/machines');
    expect(await screen.findByRole('group', { name: 'Machines toolbar' })).toBeTruthy();
  });
});
