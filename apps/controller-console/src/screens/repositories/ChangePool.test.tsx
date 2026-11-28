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

// CRSP405: the plan is read while it applies, so the bars move one step at a time.
const R1 = repository({ id: 1, full_name: 'octo/one', name: 'one', pool_on: true });
const R2 = repository({ id: 2, full_name: 'octo/two', name: 'two', pool_on: true });
const mk = (id: string, repo: number, name: string, state: string, error?: string) => ({
  id,
  action: 'pool_off',
  text: `${name} step ${id}`,
  target: name,
  repository_id: repo,
  needs_yes: false,
  state,
  ...(error ? { error } : {}),
});
const tw = (state: string, states: string[]) => ({
  id: 'p2',
  area: 'repositories',
  state,
  created_at: 'x',
  created_by: 'octo',
  steps: [
    mk('s1', 1, 'octo/one', states[0] ?? 'planned'),
    mk('s2', 1, 'octo/one', states[1] ?? 'planned'),
    mk('s3', 2, 'octo/two', states[2] ?? 'planned'),
    mk('s4', 2, 'octo/two', states[3] ?? 'planned'),
  ],
});
const bar = (name: RegExp) => screen.getByRole('progressbar', { name });

describe('Change pool progress (CRSP405)', () => {
  it('fills the bars step by step and says waiting only for a repository not started', async () => {
    const frames = [
      tw('applying', ['done', 'running']),
      tw('applying', ['done', 'done', 'running']),
      tw('done', ['done', 'done', 'done', 'done']),
    ];
    let read = 0;
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    const mock = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: [R1, R2],
        stale: { stale: false },
      })
      .on('POST', '/api/v1/repository-plans', tw('planned', []))
      .on('POST', '/api/v1/repository-plans/:id/apply', async () => {
        await gate;
        return frames[2];
      })
      .on('GET', '/api/v1/repository-plans/:id', () => {
        const f = frames[Math.min(read, frames.length - 1)];
        read += 1;
        return f;
      });
    TIMING.planPollMs = 60;
    renderApp(mock, '/setup/pool/change?to=off&repo=1&repo=2');
    fireEvent.click(
      await screen.findByRole('button', { name: /^Turn off 2 repositories/ }),
    );
    // First read: octo/one is at step 2 of 2 (one done), octo/two has not started.
    await waitFor(() => {
      expect(bar(/Turning off octo\/one/).getAttribute('aria-valuenow')).toBe('1');
      expect(bar(/Turning off octo\/one/).getAttribute('aria-valuemax')).toBe('2');
      expect(screen.getAllByText('waiting').length).toBe(1);
      expect(screen.getAllByText('octo/one step s2').length).toBeGreaterThan(0);
    });
    await waitFor(() =>
      expect(bar(/Turning off octo\/one/).getAttribute('aria-valuenow')).toBe('2'),
    );
    release();
    await waitFor(() =>
      expect(screen.getByText('2 repositories no longer use the pool.')).toBeTruthy(),
    );
    TIMING.planPollMs = 5;
  });

  it('shows the step in hand after a reload, and goes on to Done', async () => {
    let read = 0;
    const mock = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: [R1, R2],
        stale: { stale: false },
      })
      .on('GET', '/api/v1/repository-plans/:id', () => {
        read += 1;
        return read < 3
          ? tw('applying', ['done', 'done', 'done', 'running'])
          : tw('done', ['done', 'done', 'done', 'done']);
      });
    renderApp(mock, '/setup/pool/change?to=off&repo=1&repo=2&plan=p2');
    await waitFor(() =>
      expect(screen.getAllByText('octo/two step s4').length).toBeGreaterThan(0),
    );
    expect(bar(/Turning off octo\/two/).getAttribute('aria-valuenow')).toBe('1');
    expect(screen.getByRole('button', { name: 'Stop' })).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByText('2 repositories no longer use the pool.')).toBeTruthy(),
    );
  });

  it('names the step that failed, with its error', async () => {
    const failed = {
      ...tw('failed', ['done']),
      steps: [
        mk('s1', 1, 'octo/one', 'done'),
        mk('s2', 1, 'octo/one', 'failed', 'GitHub refused: a permission is missing'),
        mk('s3', 2, 'octo/two', 'skipped'),
        mk('s4', 2, 'octo/two', 'skipped'),
      ],
    };
    const mock = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: [R1, R2],
        stale: { stale: false },
      })
      .on('GET', '/api/v1/repository-plans/:id', failed);
    renderApp(mock, '/setup/pool/change?to=off&repo=1&repo=2&plan=p2');
    expect(
      await screen.findByText(
        /Nothing more was changed. GitHub refused: a permission is missing/,
      ),
    ).toBeTruthy();
    expect(screen.getAllByText(/octo\/one step s2/).length).toBeGreaterThan(0);
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
