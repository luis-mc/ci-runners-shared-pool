// Dependency setup, web (spec 4, DEP1 to DEP13): every state of "Get this machine ready" with a mocked API, the
// Review that changes nothing before the primary, the restart and undo questions, and the ten states of the
// wizard harness (UX-5, UX-13, UX-30, UX-54).
import { createApiClient } from '@crsp/contract';
import { ClockProvider } from '@crsp/ui';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  judgeAll,
  type WizardDriver,
  type WizardSnapshot,
  type WizardState,
} from '../../../../../scripts/design/wizard-behaviour';
import { ApiProvider } from '../../api/api';
import { SessionProvider } from '../../auth/session';
import { TIMING } from '../../timing';
import { MockApi, apiError, signedIn, viewer } from '../../testing/mock';
import { GetReadyWizard, type GetReadyProps } from './GetReady';
import {
  factRows,
  installLabel,
  progressLine,
  toProgressEvent,
  type DepCheck,
  type DepEvent,
  type DepJob,
} from './plan';

afterEach(cleanup);
beforeEach(() => {
  TIMING.dependencyPollMs = 10;
});

const never = () => new Promise<Response>(() => undefined);
const BASE = '/api/v1/machines/m1/dependencies';

const facts = (name: string, over: object = {}) => ({
  name,
  version: '2.0',
  source: 'docker.com',
  download_bytes: 120_000_000,
  disk_bytes: 900_000_000,
  typical_minutes: 5,
  needs_admin: true,
  restart_likely: name === 'WSL2',
  undo: `Remove ${name} from Apps.`,
  ...over,
});

const check = (over: Partial<DepCheck> = {}): DepCheck => ({
  machine_id: 'm1',
  ready: false,
  os: 'windows',
  admin: true,
  disk_free_bytes: 50_000_000_000,
  virtualization_off: false,
  items: [
    {
      id: 'wsl2',
      name: 'WSL2',
      state: 'missing',
      why: 'Containers on Windows run in WSL2.',
    },
    {
      id: 'docker-in-wsl2',
      name: 'Docker',
      state: 'missing',
      why: 'Linux runners run in a container, and containers need Docker.',
    },
  ],
  choices: [
    {
      id: 'wsl-docker',
      title: 'Docker in WSL2',
      item_ids: ['wsl2', 'docker-in-wsl2'],
      recommended: true,
      facts: [facts('WSL2'), facts('Docker')],
      requires_licence_tick: false,
      manual_steps: [
        'wsl --install --no-distribution',
        'wsl -d crsp sudo apt install docker-ce',
      ],
    },
    {
      id: 'desktop',
      title: 'Docker Desktop',
      item_ids: ['wsl2', 'docker-desktop'],
      recommended: false,
      facts: [
        facts('WSL2'),
        facts('Docker Desktop', { licence_note: 'Paid for large companies.' }),
      ],
      requires_licence_tick: true,
      manual_steps: ['winget install Docker.DockerDesktop'],
    },
  ],
  ...over,
});

const event = (over: Partial<DepEvent> = {}): DepEvent => ({
  job_id: 'j1',
  phase: 'Downloading Docker',
  phase_index: 0,
  phase_count: 3,
  phase_weight: 1,
  pct: 40,
  bytes: 48_000_000,
  total: 120_000_000,
  eta_sec: 120,
  machine: { name: 'pc', role: 'agent', os: 'Windows 11' },
  stream: 'stdout',
  line: 'got layer 1',
  ts: 1,
  ...over,
});

const job = (state: DepJob['state'], over: Partial<DepJob> = {}): DepJob => ({
  id: 'j1',
  machine_id: 'm1',
  state,
  choice_id: 'wsl-docker',
  item_ids: ['wsl2', 'docker-in-wsl2'],
  runner_os: 'linux',
  runner_kind: 'persistent',
  created_at: '2026-11-25T10:00:00Z',
  updated_at: '2026-11-25T10:01:00Z',
  added: [],
  log: [event()],
  last: event(),
  ...over,
});

const done = (over: Partial<DepJob> = {}) =>
  job('done', {
    engine_version: '27.3.1',
    added: [
      { item_id: 'wsl2', kind: 'windows-feature', ref: 'WSL', version: '2.3' },
      { item_id: 'docker-in-wsl2', kind: 'package', ref: 'docker-ce', version: '27.3.1' },
    ],
    ...over,
  });

function api(over: (a: MockApi) => void = () => undefined): MockApi {
  const a = signedIn()
    .on('POST', `${BASE}/check`, check())
    .on('GET', `${BASE}/jobs`, () => [])
    .on('POST', `${BASE}/jobs`, { ...job('installing') })
    .on('GET', `${BASE}/jobs/:id`, job('installing'))
    .on('POST', `${BASE}/jobs/:id/stop`, job('installing'))
    .on('POST', `${BASE}/jobs/:id/restart-host`, job('paused_restart'))
    .on('POST', `${BASE}/jobs/:id/undo`, job('undoing'));
  over(a);
  return a;
}

function mount(a: MockApi, props: Partial<GetReadyProps> = {}) {
  const client = createApiClient('http://controller.test', a.fetch as typeof fetch);
  const onDone = vi.fn();
  const view = render(
    <MemoryRouter>
      <ApiProvider client={client}>
        <ClockProvider>
          <SessionProvider>
            <GetReadyWizard
              machineId="m1"
              machineName="Office PC"
              runnerOs="linux"
              runnerKind="persistent"
              onDone={onDone}
              {...props}
            />
          </SessionProvider>
        </ClockProvider>
      </ApiProvider>
    </MemoryRouter>,
  );
  return { ...view, onDone };
}

const press = async (name: string | RegExp) =>
  fireEvent.click(await screen.findByRole('button', { name }));
const jobPosts = (a: MockApi) => a.count('POST', `${BASE}/jobs`);
const jobBody = (a: MockApi) =>
  a.calls.find((c) => c.method === 'POST' && c.path === `${BASE}/jobs`)?.body;

describe('words (plan.ts)', () => {
  it('computes the primary from the facts', () => {
    const c = check();
    const label = installLabel(c.choices[0]!, c.items);
    expect(label).toBe('Install WSL2 and Docker (about 10 minutes, one restart)');
  });
  it('lists the facts of an item for Review', () => {
    const names = factRows(facts('Docker', { licence_note: 'x' }) as never).map(
      (r) => r.name,
    );
    expect(names).toEqual([
      'Version',
      'Comes from',
      'Download',
      'Disk space',
      'Time',
      'Administrator',
      'Restart',
      'Licence',
      'To undo',
    ]);
  });
  it('maps an API event to the shared progress contract', () => {
    const e = toProgressEvent(event({ eta_sec: 90 }));
    expect(e).toMatchObject({ jobId: 'j1', phaseIndex: 0, etaSec: 90, stream: 'stdout' });
    expect(progressLine(e)).toBe(
      'Downloading Docker: 48.0 MB of 120 MB, 1 min 30 s left',
    );
  });
});

describe('Get this machine ready (spec 4)', () => {
  it('nothing missing: no wizard, the original task goes on (CORE-4)', async () => {
    const a = api((x) =>
      x.on('POST', `${BASE}/check`, check({ ready: true, items: [], choices: [] })),
    );
    const { onDone } = mount(a);
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
    expect(jobPosts(a)).toBe(0);
    expect(a.calls.find((c) => c.path === `${BASE}/check`)?.body).toEqual({
      runner_os: 'linux',
      runner_kind: 'persistent',
    });
  });

  it('first run on Windows: headline, facts, one primary, Do it myself, Use another machine', async () => {
    const a = api();
    const another = vi.fn();
    mount(a, { onUseAnother: another });
    await screen.findByText('This PC cannot run Linux containers yet');
    expect(screen.getByText(/containers need Docker/)).toBeTruthy();
    await screen.findByRole('button', {
      name: 'Install WSL2 and Docker (about 10 minutes, one restart)',
    });
    expect(jobPosts(a)).toBe(0);
    await press('Do it myself');
    expect(await screen.findByText(/wsl --install --no-distribution/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Use another machine' }));
    expect(another).toHaveBeenCalled();
    const before = a.count('POST', `${BASE}/check`);
    await press('Try again');
    await waitFor(() => expect(a.count('POST', `${BASE}/check`)).toBe(before + 1));
  });

  it('engine stopped: Start Docker, no install', async () => {
    const a = api((x) =>
      x.on(
        'POST',
        `${BASE}/check`,
        check({
          start_only: true,
          items: [
            {
              id: 'docker-engine',
              name: 'Docker',
              state: 'stopped',
              why: 'Docker is installed.',
            },
          ],
          choices: [
            {
              id: 'start',
              title: 'Start Docker',
              item_ids: ['docker-engine'],
              recommended: true,
              facts: [],
              requires_licence_tick: false,
              manual_steps: ['sudo service docker start'],
            },
          ],
        }),
      ),
    );
    mount(a);
    await screen.findByText('Docker is installed but not running.');
    expect(screen.queryByRole('button', { name: /^Install(?!ing)/ })).toBeNull();
    await press('Start Docker');
    await waitFor(() => expect(jobPosts(a)).toBe(1));
    expect((jobBody(a) as { choice_id: string }).choice_id).toBe('start');
  });

  it('Review lists the items and changes nothing before the primary (DEP3, DEP4)', async () => {
    const a = api();
    mount(a);
    await press(/^Install WSL2 and Docker(?!ing)/);
    await screen.findByText('Nothing has changed yet.');
    for (const w of [
      'Version',
      'Comes from',
      'Download',
      'Disk space',
      'Time',
      'Administrator',
      'Restart',
      'To undo',
    ])
      expect(screen.getAllByText(w).length).toBeGreaterThan(0);
    expect(screen.getByText(/one restart/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Install 2 items' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    expect(jobPosts(a)).toBe(0);
    await press('Install 2 items');
    await waitFor(() => expect(jobPosts(a)).toBe(1));
    expect(jobBody(a)).toMatchObject({
      choice_id: 'wsl-docker',
      confirmed: true,
    });
  });

  it('Cancel on Review goes back and still changes nothing', async () => {
    const a = api();
    mount(a);
    await press(/^Install WSL2 and Docker(?!ing)/);
    await press('Cancel');
    await screen.findByText('This PC cannot run Linux containers yet');
    expect(jobPosts(a)).toBe(0);
  });

  it('Docker Desktop needs the licence tick before the primary works', async () => {
    const a = api();
    mount(a);
    fireEvent.click(await screen.findByRole('radio', { name: /Docker Desktop/ }));
    await press(/^Install WSL2 and Docker Desktop/);
    const primary = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Install 2 items',
    });
    expect(primary.disabled).toBe(true);
    expect(screen.getByText('Tick the licence box first.')).toBeTruthy();
    fireEvent.click(
      screen.getByRole('checkbox', { name: /right to use Docker Desktop/ }),
    );
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Install 2 items' }).disabled,
    ).toBe(false);
    await press('Install 2 items');
    await waitFor(() => expect(jobPosts(a)).toBe(1));
    expect(jobBody(a)).toMatchObject({
      choice_id: 'desktop',
      accept_licence: true,
    });
  });

  it('a role below admin sees why not and cannot install', async () => {
    const a = api();
    const view = mount(a);
    void view;
    cleanup();
    const v = signedIn(viewer)
      .on('POST', `${BASE}/check`, check())
      .on('GET', `${BASE}/jobs`, () => []);
    mount(v);
    const primary = await screen.findByRole<HTMLButtonElement>('button', {
      name: /^Install WSL2 and Docker(?!ing)/,
    });
    expect(primary.disabled).toBe(true);
    expect(primary.getAttribute('aria-describedby')).toBeTruthy();
  });

  it('running: polls the job, step name, bytes and time left, Stop, busy primary (UX-13, UX-29)', async () => {
    const a = api((x) => x.on('GET', `${BASE}/jobs/:id`, job('installing')));
    mount(a);
    await press(/^Install WSL2 and Docker(?!ing)/);
    await press('Install 2 items');
    await screen.findByText('Downloading Docker: 48.0 MB of 120 MB, 2 min left', {
      selector: 'p',
    });
    const primary = screen
      .getAllByRole<HTMLButtonElement>('button', { name: 'Installing' })
      .find((b) => b.getAttribute('aria-busy') === 'true');
    expect(primary?.disabled).toBe(true);
    await waitFor(() => expect(a.count('GET', `${BASE}/jobs/j1`)).toBeGreaterThan(1));
    await press('Stop');
    await waitFor(() => expect(a.count('POST', `${BASE}/jobs/j1/stop`)).toBe(1));
    expect(screen.getByText('Show the log')).toBeTruthy();
  });

  it('later steps cannot be pressed', async () => {
    mount(api());
    await screen.findByText('This PC cannot run Linux containers yet');
    const group = screen.getByRole('group', { name: 'Get this machine ready' });
    const later = Array.from(
      group.querySelectorAll<HTMLElement>('[data-standing="later"] button'),
    );
    expect(later.length).toBe(4);
    expect(later.every((b) => (b as HTMLButtonElement).disabled)).toBe(true);
  });

  it('paused for restart: asks first, then restarts; the page goes on to Verify and Done by itself', async () => {
    let n = -1; // counts the reads after the restart was sent
    const a = api((x) => {
      x.on('GET', `${BASE}/jobs`, () => [job('paused_restart')]);
      x.on('POST', `${BASE}/jobs/:id/restart-host`, () => {
        n = 0;
        return job('paused_restart');
      });
      x.on('GET', `${BASE}/jobs/:id`, () => {
        if (n < 0) return job('paused_restart');
        n += 1;
        return n < 3 ? job('verifying') : done();
      });
    });
    mount(a);
    await screen.findByText(/Restart needed to finish/);
    expect(screen.getByRole('button', { name: 'Later' })).toBeTruthy();
    await press('Restart this PC now');
    // The question names what happens; nothing is sent yet.
    await screen.findByText(/Restart Office PC now/);
    expect(a.count('POST', `${BASE}/jobs/j1/restart-host`)).toBe(0);
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Restart this PC now',
      }),
    );
    await waitFor(() => expect(a.count('POST', `${BASE}/jobs/j1/restart-host`)).toBe(1));
    expect(
      a.calls
        .find((c) => c.path === `${BASE}/jobs/j1/restart-host`)
        ?.query.get('confirmed'),
    ).toBe('true');
    await screen.findByText(/Office PC is ready/);
  });

  it('Later leaves with the plan kept', async () => {
    const left = vi.fn();
    mount(
      api((x) => {
        x.on('GET', `${BASE}/jobs`, () => [job('paused_restart')]);
        x.on('GET', `${BASE}/jobs/:id`, job('paused_restart'));
      }),
      { onLeave: left },
    );
    await screen.findByText(/Restart needed to finish/);
    await press('Later');
    expect(left).toHaveBeenCalled();
  });

  it('stopped: Continue checks again; Undo asks first and names each item', async () => {
    const a = api((x) => {
      x.on('GET', `${BASE}/jobs`, () => []);
      x.on('GET', `${BASE}/jobs/:id`, job('stopped', { added: done().added }));
    });
    mount(a, { jobId: 'j1' });
    await screen.findByText(/Stopped\. 2 items were installed/);
    await press('Undo what was installed');
    await screen.findByText(/WSL2 2\.3: windows-feature WSL/);
    expect(screen.getByText(/Docker in WSL2 27\.3\.1: package docker-ce/)).toBeTruthy();
    expect(a.count('POST', `${BASE}/jobs/j1/undo`)).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    await waitFor(() => expect(a.count('POST', `${BASE}/jobs/j1/undo`)).toBe(1));
    expect(
      a.calls.find((c) => c.path === `${BASE}/jobs/j1/undo`)?.query.get('confirmed'),
    ).toBe('true');
    const before = a.count('POST', `${BASE}/check`);
    await press('Continue');
    await waitFor(() => expect(a.count('POST', `${BASE}/check`)).toBe(before + 1));
  });

  it('failed: what, why and what next in words; Try again, steps for myself, Undo', async () => {
    const a = api((x) =>
      x.on(
        'GET',
        `${BASE}/jobs/:id`,
        job('failed', {
          error: 'The file did not match; nothing was installed',
          manual_steps: ['wsl --install'],
          added: [{ item_id: 'wsl2', kind: 'feature', ref: 'WSL', version: '2.3' }],
        }),
      ),
    );
    mount(a, { jobId: 'j1' });
    await screen.findByText(
      /did not finish\. The file did not match; nothing was installed\. Try again/,
    );
    await press('Show the steps for myself');
    expect(await screen.findByText('wsl --install')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Undo what was installed' })).toBeTruthy();
    const before = a.count('POST', `${BASE}/check`);
    await press('Try again');
    await waitFor(() => expect(a.count('POST', `${BASE}/check`)).toBe(before + 1));
  });

  it('done: what really changed, how to undo, the engine version, back to the task', async () => {
    const a = api((x) => x.on('GET', `${BASE}/jobs/:id`, done()));
    const { onDone } = mount(a, { jobId: 'j1', doneLabel: 'Back to Add runners' });
    await screen.findByText(/Office PC is ready/);
    expect(screen.getByText('27.3.1', { selector: 'dd' })).toBeTruthy();
    expect(screen.getByText(/docker-ce/)).toBeTruthy();
    expect(screen.getByText(/To undo, remove only what is listed above/)).toBeTruthy();
    await press('Back to Add runners');
    expect(onDone).toHaveBeenCalled();
  });

  it('done after reload reads the stored job from the address', async () => {
    const a = api((x) => x.on('GET', `${BASE}/jobs/:id`, done()));
    mount(a, { jobId: 'j1' });
    await screen.findByText(/Office PC is ready/);
    expect(
      screen
        .getByRole('group', { name: 'Get this machine ready' })
        .getAttribute('data-phase'),
    ).toBe('done-after-reload');
    expect(a.count('POST', `${BASE}/check`)).toBe(0);
  });

  it('a job still going is resumed when the page opens (DEP11)', async () => {
    const a = api((x) => x.on('GET', `${BASE}/jobs`, () => [job('verifying')]));
    mount(a);
    await screen
      .findByRole('button', { name: 'Install 2 items', hidden: true })
      .catch(() => undefined);
    await waitFor(() =>
      expect(
        screen
          .getByRole('group', { name: 'Get this machine ready' })
          .getAttribute('data-phase'),
      ).toBe('running'),
    );
    expect(a.count('POST', `${BASE}/check`)).toBe(0);
  });

  it('offline machine: crsp cannot reach it, no install, Try again', async () => {
    let offline = true;
    const a = api((x) =>
      x.on('POST', `${BASE}/check`, () =>
        offline ? apiError(409, 'machine_offline', 'offline') : check(),
      ),
    );
    mount(a);
    await screen.findByText('crsp cannot reach Office PC.');
    expect(screen.queryByRole('button', { name: /^Install(?!ing)/ })).toBeNull();
    offline = false;
    await press('Try again');
    await screen.findByText('This PC cannot run Linux containers yet');
  });
});

// ---------------------------------------------------------------------------------------- the wizard harness
function read(state: WizardState, canStop: boolean): WizardSnapshot {
  const name = 'Get this machine ready';
  const group = screen.getByRole('group', { name });
  const steps = Array.from(group.querySelectorAll<HTMLElement>('.ds-steps__item')).map(
    (li) => {
      const b = li.querySelector('button') as HTMLButtonElement;
      return {
        name: (b.textContent ?? '').replace(/^[✓✕\d]+\s*/, ''),
        standing: li.getAttribute('data-standing') as 'done' | 'now' | 'later',
        pressable: !b.disabled && b.getAttribute('aria-disabled') !== 'true',
      };
    },
  );
  const bar = screen.queryByRole('region', { name: `${name} actions` }) ?? group;
  const buttons = Array.from(
    bar.querySelectorAll<HTMLButtonElement>('.ds-action-bar button'),
  ).map((b) => {
    const describedBy = b.getAttribute('aria-describedby');
    const reason = describedBy
      ? document.getElementById(describedBy)?.textContent
      : undefined;
    const part = b.getAttribute('data-part');
    const role: 'stop' | 'pause' | 'back' | 'other' =
      part === 'stop' || part === 'pause' || part === 'back' ? part : 'other';
    return {
      label: (b.textContent ?? '').trim(),
      primary: b.hasAttribute('data-primary') || part === 'primary',
      disabled: b.disabled,
      ...(reason ? { reason } : {}),
      requestOpen: b.getAttribute('aria-busy') === 'true',
      role,
    };
  });
  const doneEl = group.querySelector('[data-wizard-done]');
  return {
    state,
    steps,
    buttons,
    job: { canStop, canPause: false },
    done: {
      text: (doneEl?.textContent ?? '').trim(),
      counts: group.querySelectorAll('.ds-facts__row').length,
    },
    waitLines: new Set([
      ...group.querySelectorAll('[data-wait]'),
      ...bar.querySelectorAll('[data-wait]'),
    ]).size,
  };
}

const driver: WizardDriver = {
  id: 'get-this-machine-ready',
  async open(state) {
    const a = api();
    let props: Partial<GetReadyProps> = {};
    const stored = (j: DepJob) => {
      a.on('GET', `${BASE}/jobs/:id`, j);
      props = { jobId: 'j1' };
    };
    if (state === 'loading') a.on('POST', `${BASE}/check`, never);
    if (state === 'running') a.on('GET', `${BASE}/jobs/:id`, job('installing'));
    if (state === 'paused') stored(job('paused_restart'));
    if (state === 'stopped') stored(job('stopped', { added: done().added }));
    if (state === 'failed') stored(job('failed', { error: 'The download failed' }));
    if (state === 'done') {
      // A job that finishes while the page watches it: Done, not done after reload.
      let n = 0;
      a.on('GET', `${BASE}/jobs/:id`, () => (++n < 2 ? job('installing') : done()));
    }
    if (state === 'done-after-reload') stored(done());
    mount(a, props);
    const snap = () => read(state, state === 'running');
    if (state === 'loading') {
      await screen.findByRole('group', { name: 'Get this machine ready' });
      return snap();
    }
    if (['paused', 'stopped', 'failed', 'done-after-reload'].includes(state)) {
      await waitFor(() =>
        expect(
          screen
            .getByRole('group', { name: 'Get this machine ready' })
            .getAttribute('data-phase'),
        ).toBe(state),
      );
      return snap();
    }
    await screen.findByText('This PC cannot run Linux containers yet');
    if (state === 'first-run') return snap();
    if (state === 'planning') {
      a.on('POST', `${BASE}/check`, never);
      await press('Do it myself');
      await press('Try again');
      await screen.findByText(/Checking Office PC again/);
      return snap();
    }
    await press(/^Install WSL2 and Docker(?!ing)/);
    await screen.findByText('Nothing has changed yet.');
    if (state === 'ready-to-review') return snap();
    await press('Install 2 items');
    if (state === 'running') {
      await screen.findByText(/Downloading Docker/, { selector: 'p' });
      return snap();
    }
    await screen.findByText(/Office PC is ready/);
    return snap();
  },
};

describe('the wizard harness', () => {
  it('passes the ten states with an empty known list', async () => {
    for (const state of [
      'loading',
      'first-run',
      'planning',
      'ready-to-review',
      'running',
      'paused',
      'stopped',
      'failed',
      'done',
      'done-after-reload',
    ] as const) {
      cleanup();
      expect((await driver.open(state)).state).toBe(state);
    }
    cleanup();
    const problems = await judgeAll([
      {
        id: driver.id,
        open: async (s) => {
          cleanup();
          return driver.open(s);
        },
      },
    ]);
    expect(problems.join('\n')).toBe('');
  });
});
