// LOOK31: the six-item menu, Shared Pool and its tabs, the old addresses, the Runners grouping, the
// "How crsp works" strip and the Jobs sub-sections.
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { renderApp, repository, runner, signedIn } from '../../testing/mock';
import { groupByMachine, runnerSummary } from '../runners/Runners';
import { showStrip, STRIP_STORE_NAME } from '../overview/HowCrspWorks';
import { poolGives, runnerKinds } from './SetupSummary';

afterEach(cleanup);

/** The summary line of a page: its whole text, since the dots and the dash are their own nodes. */
const summary = (text: string) =>
  screen.findByText((_, el) => el?.className === 'ds-summary' && el.textContent === text);
beforeEach(() => {
  try {
    window.localStorage.clear();
  } catch {
    // No storage in this environment: nothing to clear.
  }
});

const repos = (items: unknown[]) => ({ items, stale: { stale: false } });
const ready = () =>
  signedIn()
    .on('GET', '/api/v1/runners', { items: [runner()] })
    .on('GET', '/api/v1/github/repositories', repos([repository({ pool_on: true })]));
const empty = () =>
  signedIn()
    .on('GET', '/api/v1/machines', { items: [] })
    .on('GET', '/api/v1/runners', { items: [] })
    .on('GET', '/api/v1/github/repositories', repos([]));

describe('Setup > Summary loads each card on its own (CRSP389)', () => {
  it('shows Machines and Repositories while the runner list is still loading', async () => {
    let release: (v: unknown) => void = () => undefined;
    const held = new Promise((r) => (release = r));
    const api = signedIn()
      .on('GET', '/api/v1/runners', () => held)
      .on('GET', '/api/v1/github/repositories', repos([repository({ pool_on: true })]));
    renderApp(api, '/setup');
    expect(await screen.findByText('1 repository uses the pool.')).toBeTruthy();
    // The Runners card alone says it is loading, with no count yet.
    expect(screen.getAllByText('Loading the pool figures.')).toHaveLength(1);
    expect(screen.getByText('Open Machines')).toBeTruthy();
    release({ items: [runner()] });
    await waitFor(() =>
      expect(screen.queryByText('Loading the pool figures.')).toBeNull(),
    );
  });
});

describe('the seven areas and the Setup tabs (LOOK56)', () => {
  it.each([
    ['/setup/machines', 'Machines'],
    ['/setup/runners', 'Runners'],
    ['/setup/images', 'Images and caches'],
    ['/setup/size', 'Size and backends'],
  ])('%s opens its Setup page', async (from, title) => {
    renderApp(ready(), from);
    expect(await screen.findByRole('tab', { name: title, selected: true })).toBeTruthy();
    expect(
      screen.getAllByRole('heading', { level: 1 }).map((h) => h.textContent),
    ).toEqual(['Setup']);
    const nav = screen.getByRole('navigation', { name: 'Areas' });
    expect(
      within(nav).getByRole('link', { name: 'Setup' }).getAttribute('aria-current'),
    ).toBe('page');
  });

  it('opens Setup > Summary from /setup', async () => {
    renderApp(ready(), '/setup');
    expect(
      await screen.findByRole('tab', { name: 'Summary', selected: true }),
    ).toBeTruthy();
  });

  it('shows the Setup tabs and goes to a tab with the keyboard', async () => {
    renderApp(ready(), '/setup/summary');
    const tabs = await screen.findByRole('tablist', { name: 'Setup sections' });
    expect(
      within(tabs)
        .getAllByRole('tab')
        .map((t) => t.textContent),
    ).toEqual([
      'Summary',
      'Machines',
      'Runners',
      'Images and caches',
      'Size and backends',
      'Pool setup',
    ]);
    const first = within(tabs).getByRole('tab', { name: 'Summary' });
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowRight' });
    expect(
      await screen.findByRole('tab', { name: 'Machines', selected: true }),
    ).toBeTruthy();
    // Choosing a tab redraws the row; focus is back on the chosen tab a frame later (keepFocus), so wait for it.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(screen.getByRole('tablist', { name: 'Setup sections' })).getByRole('tab', {
          name: 'Machines',
        }),
      ),
    );
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'End' });
    expect(
      await screen.findByRole('tab', { name: 'Pool setup', selected: true }),
    ).toBeTruthy();
  });

  it('moves between the seven areas with Down and j from the menu', async () => {
    const user = userEvent.setup();
    renderApp(ready(), '/');
    const nav = await screen.findByRole('navigation', { name: 'Areas' });
    const links = within(nav).getAllByRole('link');
    expect(links).toHaveLength(7);
    links[0]!.focus();
    await user.keyboard('j');
    expect(document.activeElement).toBe(links[1]);
    await user.keyboard('{ArrowDown}');
    expect(document.activeElement).toBe(links[2]);
    await user.keyboard('k');
    expect(document.activeElement).toBe(links[1]);
  });
});

describe('Shared Pool summary and first-run guide (D33)', () => {
  it('shows the three cards and the steps; an empty pool has its next action', async () => {
    renderApp(empty(), '/setup/summary');
    expect(await screen.findByRole('region', { name: 'Setup steps' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Machines' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Runners' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Repositories' })).toBeTruthy();
    expect(screen.getByText('Add a machine').closest('a')?.getAttribute('href')).toBe(
      '/setup/machines/add',
    );
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Choose repositories' })
        .disabled,
    ).toBe(true);
  });

  it('each card has an Open link and the steps are done on a set up pool', async () => {
    renderApp(ready(), '/setup/summary');
    const link = await screen.findByRole('link', { name: 'Open Machines' });
    expect(link.getAttribute('href')).toBe('/setup/machines');
    expect(screen.getByRole('link', { name: 'Open Runners' }).getAttribute('href')).toBe(
      '/setup/runners',
    );
    expect(
      screen.getByRole('link', { name: 'Open Pool setup' }).getAttribute('href'),
    ).toBe('/setup/pool');
    expect(screen.getByText('1 repository uses the pool.')).toBeTruthy();
    expect(await summary('1 runner — 1 persistent · 0 ephemeral')).toBeTruthy();
  });

  it('words the cards', () => {
    expect(runnerKinds([])).toEqual({
      total: [{ count: 0, word: 'runner', plural: 'runners' }],
      states: [
        { count: 0, word: 'persistent' },
        { count: 0, word: 'ephemeral' },
      ],
    });
    expect(poolGives([])).toBe('0 machines, pool size not set.');
  });
});

describe('Runners grouped by machine (D32)', () => {
  const items = [
    runner({
      id: 'r1',
      name: 'a-1',
      machine_id: 'm2',
      machine_name: 'win-1',
      os: 'windows',
    }),
    runner({
      id: 'r2',
      name: 'b-1',
      machine_id: 'm1',
      machine_name: 'build-1',
      state: 'busy',
    }),
    runner({ id: 'r3', name: 'b-2', machine_id: 'm1', machine_name: 'build-1' }),
  ];

  it('counts the pool and groups by machine in name order', () => {
    expect(runnerSummary(items).total.map((t) => t.count)).toEqual([2, 3]);
    const g = groupByMachine(items);
    expect(g.map((x) => [x.name, x.rows.length])).toEqual([
      ['build-1', 2],
      ['win-1', 1],
    ]);
  });

  it('shows the totals, one table per machine and the repository of a busy runner', async () => {
    const busy = runner({
      id: 'r2',
      name: 'b-1',
      machine_id: 'm1',
      machine_name: 'build-1',
      state: 'busy',
      current_job: {
        repository: 'octo/app',
        job_name: 'test',
        started_at: '2026-11-18T00:00:00Z',
      },
    } as never);
    const api = ready().on('GET', '/api/v1/runners', {
      items: [items[0]!, busy, items[2]!],
    });
    renderApp(api, '/setup/runners');
    expect(
      await summary('2 machines · 3 runners — 2 idle · 1 busy · 0 not ready'),
    ).toBeTruthy();
    expect(screen.queryByText('Not ready')).toBeNull();
    const t1 = await screen.findByRole('table', { name: 'Runners on build-1' });
    expect(screen.getByRole('table', { name: 'Runners on win-1' })).toBeTruthy();
    expect(within(t1).getByText(/octo\/app test/)).toBeTruthy();
    expect(within(t1).queryByRole('columnheader', { name: 'Machine' })).toBeNull();
  });
});

describe('First-run job path (D33, LOOK44)', () => {
  it('decides from the three counts and the dismissal', () => {
    const f = { machines: 1, runners: 1, repositories: 1, dismissed: false };
    expect(showStrip(f)).toBe(false);
    expect(showStrip({ ...f, machines: 0 })).toBe(true);
    expect(showStrip({ ...f, runners: 0 })).toBe(true);
    expect(showStrip({ ...f, repositories: 0 })).toBe(true);
    expect(showStrip({ ...f, machines: 0, dismissed: true })).toBe(false);
  });

  it('shows on first run and is dismissed for good', async () => {
    renderApp(empty(), '/');
    expect(
      await screen.findByRole('region', { name: 'How a job gets run' }),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open Help' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Hide this' }));
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'How a job gets run' })).toBeNull(),
    );
    expect(window.localStorage.getItem(STRIP_STORE_NAME)).toBe('1');
    cleanup();
    renderApp(empty(), '/');
    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'How a job gets run' })).toBeNull(),
    );
  });

  it('is gone once the pool has a machine, a runner and a repository', async () => {
    const api = ready();
    renderApp(api, '/');
    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    // The strip decides once its three lists are answered: wait on that, then let the state settle.
    await waitFor(() => {
      expect(api.count('GET', '/api/v1/machines')).toBeGreaterThan(0);
      expect(api.count('GET', '/api/v1/runners')).toBeGreaterThan(0);
      expect(api.count('GET', '/api/v1/github/repositories')).toBeGreaterThan(0);
    });
    await act(async () => {});
    expect(screen.queryByRole('region', { name: 'How a job gets run' })).toBeNull();
  });

  it('stays while a repository is still to choose', async () => {
    const api = ready().on('GET', '/api/v1/github/repositories', repos([]));
    renderApp(api, '/');
    expect(
      await screen.findByRole('region', { name: 'How a job gets run' }),
    ).toBeTruthy();
  });
});
