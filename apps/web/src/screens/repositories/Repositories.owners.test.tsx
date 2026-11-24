// LOOK5 (LOOK D15, D16): the list is grouped by owner and lists only repositories of an active type.
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

describe('Repositories grouped by owner', () => {
  it('has no Owner column; each owner is a heading with its counts and one table', async () => {
    renderApp(app(), '/setup/pool');
    const acme = await screen.findByRole('table', { name: 'acme repositories' });
    expect(screen.getByRole('table', { name: 'octo repositories' })).toBeTruthy();
    expect(within(acme).queryByRole('columnheader', { name: 'Owner' })).toBeNull();
    expect(screen.getByText('2 repositories · 1 use the pool')).toBeTruthy();
    expect(screen.getByText('1 repository · 0 use the pool')).toBeTruthy();
  });

  it('selects the whole group with its tick box', async () => {
    renderApp(app(), '/setup/pool');
    await screen.findByRole('table', { name: 'acme repositories' });
    fireEvent.click(screen.getByRole('checkbox', { name: /^acme/ }));
    expect(screen.getByText('2 selected')).toBeTruthy();
    expect(screen.getByRole('checkbox', { name: 'Select acme/api' })).toHaveProperty(
      'checked',
      true,
    );
    expect(screen.getByRole('checkbox', { name: 'Select octo/app' })).toHaveProperty(
      'checked',
      false,
    );
  });

  it('searches across groups and hides a group with no match', async () => {
    renderApp(app(), '/setup/pool');
    await screen.findByRole('table', { name: 'acme repositories' });
    fireEvent.change(screen.getByRole('textbox', { name: 'Search repositories' }), {
      target: { value: 'app' },
    });
    expect(screen.queryByRole('table', { name: 'acme repositories' })).toBeNull();
    expect(screen.getByRole('table', { name: 'octo repositories' })).toBeTruthy();
    fireEvent.change(screen.getByRole('textbox', { name: 'Search repositories' }), {
      target: { value: 'a' },
    });
    expect(screen.getByRole('table', { name: 'acme repositories' })).toBeTruthy();
    expect(screen.getByRole('table', { name: 'octo repositories' })).toBeTruthy();
  });
});

describe('Repositories: inactive types are not listed', () => {
  it('lists private only, says how many are not listed, and counts private ones', async () => {
    renderApp(app(), '/setup/pool');
    await screen.findByRole('table', { name: 'octo repositories' });
    expect(screen.queryByRole('link', { name: 'site' })).toBeNull();
    expect(screen.queryByRole('table', { name: 'pub repositories' })).toBeNull();
    expect(
      screen.getByText(
        /2 public repositories are not listed: the pool serves private repositories only\./,
      ),
    ).toBeTruthy();
    expect(screen.getByText('3 private repositories')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Select all 3' }));
    expect(screen.getByText('3 selected')).toBeTruthy();
  });

  it('List them too shows them in grey in their groups and the line offers to hide them', async () => {
    renderApp(app(), '/setup/pool');
    fireEvent.click(await screen.findByRole('link', { name: 'List them too' }));
    const octo = await screen.findByRole('table', { name: 'octo repositories' });
    const row = within(octo)
      .getAllByRole('row')
      .find((r) => within(r).queryByRole('link', { name: 'site' }));
    expect(row?.getAttribute('data-inactive')).toBe('true');
    expect(screen.getByRole('table', { name: 'pub repositories' })).toBeTruthy();
    expect(screen.getByText(/2 public repositories are listed in grey/)).toBeTruthy();
    fireEvent.click(screen.getByRole('link', { name: 'Hide them' }));
    expect(await screen.findByRole('link', { name: 'List them too' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'site' })).toBeNull();
  });

  it('the note has List them too and no Change active types link', async () => {
    renderApp(app(), '/setup/pool');
    expect(await screen.findByRole('link', { name: 'List them too' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Change active types' })).toBeNull();
  });

  it('shows no note and lists everything when public repositories are on', async () => {
    renderApp(app(true), '/setup/pool');
    await screen.findByRole('table', { name: 'pub repositories' });
    expect(screen.queryByText(/are not listed/)).toBeNull();
    expect(screen.getByText('5 private & public repositories')).toBeTruthy();
  });
});
