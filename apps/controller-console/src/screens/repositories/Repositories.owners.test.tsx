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

describe('Repositories in three groups (CRSP340)', () => {
  it('puts each repository in its group with one card each and the count in the title', async () => {
    renderApp(app(), '/setup/pool');
    const using = await screen.findByRole('region', { name: /^Using the pool/ });
    expect(within(using).getByRole('link', { name: 'acme/api' })).toBeTruthy();
    const not = screen.getByRole('region', { name: /^Not using the pool/ });
    expect(within(not).getByRole('link', { name: 'acme/web' })).toBeTruthy();
    expect(within(not).getByRole('link', { name: 'octo/app' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: /^Needs you/ })).toBeNull();
  });

  it('ticks cards of one group and turns the group action into a count', async () => {
    renderApp(app(), '/setup/pool');
    const not = await screen.findByRole('region', { name: /^Not using the pool/ });
    const turnOn = within(not).getByRole<HTMLButtonElement>('button', {
      name: 'Turn on',
    });
    expect(turnOn.disabled).toBe(true);
    fireEvent.click(within(not).getByRole('checkbox', { name: 'Select acme/web' }));
    fireEvent.click(within(not).getByRole('checkbox', { name: 'Select octo/app' }));
    expect(
      within(not).getByRole<HTMLButtonElement>('button', { name: 'Turn on 2' }).disabled,
    ).toBe(false);
  });

  it('searches across the groups', async () => {
    renderApp(app(), '/setup/pool');
    await screen.findByRole('region', { name: /^Using the pool/ });
    fireEvent.change(screen.getByRole('textbox', { name: 'Search repositories' }), {
      target: { value: 'app' },
    });
    expect(screen.queryByRole('link', { name: 'acme/api' })).toBeNull();
    expect(screen.getByRole('link', { name: 'octo/app' })).toBeTruthy();
  });
});

describe('Repositories: inactive types are not listed', () => {
  it('lists private only, says how many are not listed, and counts private ones', async () => {
    renderApp(app(), '/setup/pool');
    await screen.findByRole('region', { name: /^Not using the pool/ });
    expect(screen.queryByRole('link', { name: 'octo/site' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'pub/open' })).toBeNull();
    expect(
      screen.getByText(
        /2 public repositories are not listed: the pool serves private repositories only\./,
      ),
    ).toBeTruthy();
    expect(screen.getByText('3 private repositories')).toBeTruthy();
  });

  it('List them too shows them locked with the reason and the line offers to hide them', async () => {
    renderApp(app(), '/setup/pool');
    fireEvent.click(await screen.findByRole('link', { name: 'List them too' }));
    expect(await screen.findByRole('link', { name: 'octo/site' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'pub/open' })).toBeTruthy();
    expect(
      screen.getByRole<HTMLInputElement>('checkbox', { name: /^Select pub[/]open/ })
        .disabled,
    ).toBe(true);
    expect(screen.getByText(/2 public repositories are listed in grey/)).toBeTruthy();
    fireEvent.click(screen.getByRole('link', { name: 'Hide them' }));
    expect(await screen.findByRole('link', { name: 'List them too' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'octo/site' })).toBeNull();
  });

  it('the note has List them too and no Change active types link', async () => {
    renderApp(app(), '/setup/pool');
    expect(await screen.findByRole('link', { name: 'List them too' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Change active types' })).toBeNull();
  });

  it('shows no note and lists everything when public repositories are on', async () => {
    renderApp(app(true), '/setup/pool');
    await screen.findByRole('link', { name: 'pub/open' });
    expect(screen.queryByText(/are not listed/)).toBeNull();
    expect(screen.getByText('5 private & public repositories')).toBeTruthy();
  });
});
