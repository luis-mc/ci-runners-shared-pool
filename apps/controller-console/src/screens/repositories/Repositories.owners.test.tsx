// LOOK5 (LOOK D15, D16), CRSP340: the cards sit in three groups and only repositories of an active type are listed.
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, repository, signedIn, type MockApi } from '../../testing/mock';

afterEach(cleanup);

const items = [
  repository({ id: 1, full_name: 'acme/api', owner: 'acme', name: 'api', pool_on: true }),
  repository({ id: 2, full_name: 'acme/web', owner: 'acme', name: 'web' }),
  repository({ id: 3, full_name: 'octo/app', owner: 'octo', name: 'app' }),
  repository({
    id: 4,
    full_name: 'octo/site',
    owner: 'octo',
    name: 'site',
    visibility: 'public',
  }),
  repository({
    id: 5,
    full_name: 'pub/open',
    owner: 'pub',
    name: 'open',
    visibility: 'public',
  }),
];

function app(enabled = false): MockApi {
  return signedIn()
    .on('GET', '/api/v1/settings/public-repositories', { enabled, public_on_pool: 0 })
    .on('GET', '/api/v1/github/repositories', { items, stale: { stale: false } });
}

describe('Repositories in three groups (CRSP359)', () => {
  it('puts each repository in its group with one tick row each and the count in the title', async () => {
    renderApp(app(), '/setup/pool');
    const using = await screen.findByRole('region', { name: /^Using the pool/ });
    expect(within(using).getByRole('checkbox', { name: 'acme/api' })).toBeTruthy();
    const not = screen.getByRole('region', { name: /^Not using the pool/ });
    expect(within(not).getByRole('checkbox', { name: 'acme/web' })).toBeTruthy();
    expect(within(not).getByRole('checkbox', { name: 'octo/app' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: /^Needs you/ })).toBeNull();
  });

  it('ticks rows of one group and turns the bar action into a count', async () => {
    renderApp(app(), '/setup/pool');
    const not = await screen.findByRole('region', { name: /^Not using the pool/ });
    const bar = screen.getByRole('region', { name: 'Actions' });
    expect(
      within(bar).getByRole<HTMLButtonElement>('button', { name: 'Turn on' }).disabled,
    ).toBe(true);
    fireEvent.click(within(not).getByRole('checkbox', { name: 'acme/web' }));
    fireEvent.click(within(not).getByRole('checkbox', { name: 'octo/app' }));
    expect(
      within(bar).getByRole<HTMLButtonElement>('button', { name: 'Turn on 2' }).disabled,
    ).toBe(false);
  });

  it('has no search and no pager: every row of a group is listed', async () => {
    renderApp(app(), '/setup/pool');
    await screen.findByRole('region', { name: /^Using the pool/ });
    expect(screen.queryByRole('textbox', { name: 'Search repositories' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Show all/ })).toBeNull();
  });
});

describe('Repositories: inactive types are not listed', () => {
  it('lists private only and says how many are not listed, with a way to the setting', async () => {
    renderApp(app(), '/setup/pool');
    await screen.findByRole('region', { name: /^Not using the pool/ });
    expect(screen.queryByRole('checkbox', { name: 'octo/site' })).toBeNull();
    expect(screen.queryByRole('checkbox', { name: 'pub/open' })).toBeNull();
    expect(
      screen.getByText(
        /2 public repositories are not listed: the pool serves private repositories only\./,
      ),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open the setting' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'List them too' })).toBeNull();
  });

  it('shows no note and lists everything when public repositories are on', async () => {
    renderApp(app(true), '/setup/pool');
    await screen.findByRole('checkbox', { name: 'pub/open' });
    expect(screen.queryByText(/are not listed/)).toBeNull();
    expect(
      screen.getByRole<HTMLInputElement>('checkbox', { name: 'pub/open' }).disabled,
    ).toBe(false);
  });
});
