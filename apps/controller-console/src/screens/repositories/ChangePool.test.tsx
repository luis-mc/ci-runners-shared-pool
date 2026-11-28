// CRSP340: the Turn on, Turn off and Repair wizard shows each step as a command and its result in the Terminal.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { TIMING } from '../../timing';
import { MockApi, renderApp, repository, signedIn } from '../../testing/mock';
import { groupsOf } from './PoolGroups';

afterEach(cleanup);
TIMING.planPollMs = 5;

const step = (state: string) => ({
  id: 's1',
  action: 'pool_off',
  text: 'Remove the variable from octo/live',
  target: 'octo/live',
  needs_yes: false,
  state,
});
const plan = (state: string, s: string) => ({
  id: 'p1',
  area: 'repositories',
  state,
  created_at: 'x',
  created_by: 'octo',
  steps: [step(s)],
});
const live = repository({ id: 9, full_name: 'octo/live', name: 'live', pool_on: true });

function api(): MockApi {
  return signedIn()
    .on('GET', '/api/v1/github/repositories', { items: [live], stale: { stale: false } })
    .on('POST', '/api/v1/repository-plans', plan('planned', 'planned'))
    .on('POST', '/api/v1/repository-plans/:id/apply', plan('applying', 'running'))
    .on('GET', '/api/v1/repository-plans/:id', plan('done', 'done'));
}

describe('Change pool wizard', () => {
  it('reviews, runs and shows the command and its result in the terminal', async () => {
    renderApp(api(), '/setup/pool/change?to=off&repo=9');
    expect(await screen.findByText('These will stop using the pool')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /^Turn off 1 repository/ }));
    expect(
      (await screen.findAllByText(/Remove the variable from octo\/live/)).length,
    ).toBeGreaterThan(0);
    await waitFor(() =>
      expect(screen.getByText('1 repository no longer uses the pool.')).toBeTruthy(),
    );
  });

  it('repair asks the controller to repair and shows its steps (CRSP388)', async () => {
    const bodies: unknown[] = [];
    const write = {
      id: 's1',
      action: 'write_variable',
      text: 'Write variable CRSP_RUNS_ON_LINUX of octo/live = ["self-hosted","linux"] (it is missing)',
      target: 'CRSP_RUNS_ON_LINUX',
      needs_yes: false,
      state: 'planned',
    };
    const planned = { ...plan('planned', 'planned'), steps: [write] };
    const mock = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: [live],
        stale: { stale: false },
      })
      .on('POST', '/api/v1/repository-plans', (req: { body?: unknown }) => {
        bodies.push(req.body);
        return planned;
      });
    renderApp(mock, '/setup/pool/change?to=repair&repo=9');
    expect(await screen.findByText('These will be repaired')).toBeTruthy();
    expect(bodies[0]).toEqual({ changes: [{ repository_id: 9, pool: 'repair' }] });
  });

  it('repair with nothing different finishes Done with a plain line, never waiting (CRSP388)', async () => {
    const done = { ...plan('done', 'done'), steps: [] };
    const mock = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: [live],
        stale: { stale: false },
      })
      .on('POST', '/api/v1/repository-plans', done);
    renderApp(mock, '/setup/pool/change?to=repair&repo=9');
    expect(
      await screen.findByText('Nothing to repair: the variables are already right.'),
    ).toBeTruthy();
    expect(screen.queryByText(/Nothing more was changed/)).toBeNull();
    expect(screen.queryByText('waiting')).toBeNull();
    expect(screen.getByText('using the pool')).toBeTruthy();
  });

  it('says so when no repository was chosen', async () => {
    renderApp(api(), '/setup/pool/change?to=on');
    expect(await screen.findByText(/No repository was chosen/)).toBeTruthy();
  });
});

describe('groupsOf', () => {
  it('splits by pool and by a broken variable', () => {
    const a = repository({ id: 1, pool_on: true });
    const b = repository({ id: 2, pool_on: true });
    const c = repository({ id: 3, pool_on: false });
    const g = groupsOf([a, b, c], (r) => r.id === 2);
    expect(g.good.map((r) => r.id)).toEqual([1]);
    expect(g.bad.map((r) => r.id)).toEqual([2]);
    expect(g.not.map((r) => r.id)).toEqual([3]);
  });
});
