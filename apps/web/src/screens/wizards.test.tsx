// UX-5, UX-30, UX-54, R5.19: every wizard (Connect GitHub, Add a machine, Turn on the pool, Add runners) is driven
// through the ten states of the wizard harness. The page passes data only; the shared wizard owns the step bar,
// the locks, Stop, busy and Done. The harness reads each state from the DOM and judges it.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  judgeAll,
  type WizardDriver,
  type WizardSnapshot,
  type WizardState,
} from '../../../../scripts/design/wizard-behaviour';
import { TIMING } from '../timing';
import { MockApi, machine, renderApp, repository, signedIn } from '../testing/mock';
import { createApiClient } from '@crsp/contract';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ApiProvider } from '../api/api';
import { ClockProvider } from '@crsp/ui';
import { SessionProvider } from '../auth/session';
import { ConnectGithubWizard } from './setup/Setup';

afterEach(cleanup);
beforeEach(() => {
  TIMING.joinPollMs = 10;
  TIMING.planPollMs = 10;
});

const never = () => new Promise<Response>(() => undefined);

/** Read what the harness asks for from the rendered wizard. */
function read(state: WizardState, name: string, canStop: boolean): WizardSnapshot {
  const group = screen.getByRole('group', { name });
  const steps = Array.from(group.querySelectorAll<HTMLElement>('.ds-steps__item')).map(
    (li) => {
      const b = li.querySelector('button') as HTMLButtonElement;
      return {
        name: (b.textContent ?? '').replace(/^[✓\d]+\s*/, ''),
        standing: li.getAttribute('data-standing') as 'done' | 'now' | 'later',
        pressable: !b.disabled && b.getAttribute('aria-disabled') !== 'true',
      };
    },
  );
  // The bar is drawn in the main column by `Frame`, not inside the wizard box: find it by its name.
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
      primary:
        b.hasAttribute('data-primary') || b.getAttribute('data-part') === 'primary',
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

const step = (name: string) =>
  waitFor(() => expect(screen.getByRole('group', { name })).toBeTruthy());
const press = async (label: string | RegExp) =>
  fireEvent.click(await screen.findByRole('button', { name: label }));

// ---------------------------------------------------------------------------------------------------- Add a machine
const joinCode = (state: string, extra: object = {}) => ({
  id: 'j1',
  state,
  created_at: '2026-11-14T10:00:00Z',
  expires_at: '2099-11-14T10:15:00Z',
  ...extra,
});

function machineApi(): MockApi {
  return signedIn()
    .on('GET', '/api/v1/machines', { items: [] })
    .on('GET', '/api/v1/machines/:id', machine())
    .on('GET', '/api/v1/install-command', {
      controller_address: 'https://controller:8460',
      agent_port: 8461,
      cert_sha256: 'ab',
      items: [
        { os: 'linux', command: 'curl -fsSL https://controller:8460/install | sh' },
      ],
    })
    .on('POST', '/api/v1/join-codes', {
      code: 'ABCD-1234',
      join_code: joinCode('waiting'),
      commands: {
        controller_address: 'x',
        agent_port: 1,
        cert_sha256: 'x',
        items: [{ os: 'linux', command: 'curl ... --code ABCD-1234' }],
      },
    })
    .on('DELETE', '/api/v1/join-codes/:id', undefined);
}

/** A lone repository is ticked already (finding 11): tick it only if it is not. */
async function tickOnce(name: string): Promise<void> {
  const box = await screen.findByRole<HTMLInputElement>('checkbox', { name });
  if (!box.checked) fireEvent.click(box);
}

const STORED =
  /Start again|Try again|Open Runners|Open Repositories|Open Machines|Sign in with GitHub/;

const machineDriver: WizardDriver = {
  id: 'add-machine',
  async open(state) {
    const api = machineApi();
    let path = '/setup/machines/add';
    if (state === 'loading') api.on('GET', '/api/v1/machines', never);
    if (state === 'planning') api.on('GET', '/api/v1/install-command', never);
    if (['paused', 'stopped', 'failed', 'done-after-reload'].includes(state)) {
      path += '?join=j1';
      const stored = {
        paused: joinCode('waiting'),
        stopped: joinCode('revoked'),
        failed: joinCode('expired'),
      }[state as 'paused'];
      api.on(
        'GET',
        '/api/v1/join-codes/:id',
        stored ?? joinCode('joined', { machine_id: 'm1' }),
      );
    }
    api.on('GET', '/api/v1/join-codes/:id', (_c, _p) =>
      joinCode(
        state === 'done' ? 'joined' : 'waiting',
        state === 'done' ? { machine_id: 'm1' } : {},
      ),
    );
    if (path.includes('join=')) {
      const stored = {
        paused: joinCode('waiting'),
        stopped: joinCode('revoked'),
        failed: joinCode('expired'),
      }[state as 'paused'];
      api.on(
        'GET',
        '/api/v1/join-codes/:id',
        stored ?? joinCode('joined', { machine_id: 'm1' }),
      );
    }
    renderApp(api, path);
    await step('Add a machine');
    const snap = () => read(state, 'Add a machine', state === 'running');
    if (state === 'loading') return snap();
    if (path.includes('join=')) {
      await screen.findByRole('button', { name: STORED });
      return snap();
    }
    await screen.findByRole('radio', { name: 'Linux' });
    if (state === 'first-run') return snap();
    await press('Next');
    if (state === 'planning') return snap();
    await screen.findByText(/Nothing has changed yet/);
    if (state === 'ready-to-review') return snap();
    await press('Join a machine');
    if (state === 'running') {
      await screen.findByText(/Run this command on the machine now/);
      return snap();
    }
    await screen.findByText('The machine joined the pool.');
    return snap();
  },
};

// ---------------------------------------------------------------------------------------------------- Turn on the pool
const plan = (state: string, steps: object[] = []) => ({
  id: 'p1',
  area: 'repositories',
  state,
  created_at: '2026-11-14T10:00:00Z',
  created_by: 'octo',
  steps: steps.length
    ? steps
    : [
        {
          id: 's1',
          action: 'pool_on',
          text: 'Turn on octo/app',
          needs_yes: false,
          state: state === 'done' ? 'done' : state === 'failed' ? 'failed' : 'planned',
          ...(state === 'failed' ? { error: 'GitHub refused the variable.' } : {}),
        },
        {
          id: 's2',
          action: 'write_variable',
          text: 'Write RUNS_ON_LINUX',
          needs_yes: false,
          state: state === 'done' ? 'done' : 'planned',
        },
      ],
});

const poolDriver: WizardDriver = {
  id: 'turn-on-pool',
  async open(state) {
    const api = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: [repository()],
        stale: { stale: false },
      })
      .on('POST', '/api/v1/repository-plans', plan('planned'))
      .on('POST', '/api/v1/repository-plans/:id/apply', plan('applying'))
      .on('GET', '/api/v1/repository-plans/:id', plan('done'));
    let path = '/setup/pool/turn-on';
    if (state === 'loading') api.on('GET', '/api/v1/github/repositories', never);
    if (state === 'planning') api.on('POST', '/api/v1/repository-plans', never);
    if (state === 'running')
      api.on('GET', '/api/v1/repository-plans/:id', plan('applying'));
    if (['paused', 'stopped', 'failed', 'done-after-reload'].includes(state)) {
      path += '?plan=p1';
      api.on(
        'GET',
        '/api/v1/repository-plans/:id',
        plan(
          { paused: 'applying', stopped: 'stopped', failed: 'failed' }[
            state as 'paused'
          ] ?? 'done',
        ),
      );
    }
    renderApp(api, path);
    await step('Turn on the pool');
    const snap = () => read(state, 'Turn on the pool', state === 'running');
    if (state === 'loading') return snap();
    if (path.includes('plan=')) {
      await screen.findByRole('button', { name: STORED });
      return snap();
    }
    await tickOnce('Select octo/app');
    if (state === 'first-run') return snap();
    await press('Next');
    if (state === 'planning') return snap();
    await screen.findByText(/Nothing has changed yet/);
    if (state === 'ready-to-review') return snap();
    await press('Turn on 1 repository');
    if (state === 'running') {
      await screen.findByText(/Turning on/, { selector: 'p' });
      return snap();
    }
    await screen.findByText(/The pool is on/);
    return snap();
  },
};

// ---------------------------------------------------------------------------------------------------- Add runners
const rplan = (state: string) => ({ ...plan(state), id: 'rp1', area: 'runners' });

const runnersDriver: WizardDriver = {
  id: 'add-runners',
  async open(state) {
    const api = signedIn()
      .on('GET', '/api/v1/machines', { items: [machine()] })
      .on('GET', '/api/v1/github/repositories', {
        items: [repository({ pool_on: true })],
        stale: { stale: false },
      })
      .on('POST', '/api/v1/runner-plans', rplan('planned'))
      .on('POST', '/api/v1/runner-plans/:id/apply', rplan('applying'))
      .on('GET', '/api/v1/runner-plans/:id', rplan('done'));
    let path = '/setup/runners/add';
    if (state === 'loading') api.on('GET', '/api/v1/machines', never);
    if (state === 'planning') api.on('POST', '/api/v1/runner-plans', never);
    if (state === 'running') api.on('GET', '/api/v1/runner-plans/:id', rplan('applying'));
    if (['paused', 'stopped', 'failed', 'done-after-reload'].includes(state)) {
      path += '?plan=rp1';
      api.on(
        'GET',
        '/api/v1/runner-plans/:id',
        rplan(
          { paused: 'applying', stopped: 'stopped', failed: 'failed' }[
            state as 'paused'
          ] ?? 'done',
        ),
      );
    }
    renderApp(api, path);
    await step('Add runners');
    const snap = () => read(state, 'Add runners', state === 'running');
    if (state === 'loading') return snap();
    if (path.includes('plan=')) {
      await screen.findByRole('button', { name: STORED });
      return snap();
    }
    await screen.findByRole('textbox', { name: /Labels/ });
    if (state === 'first-run') return snap();
    await press('Next');
    if (state === 'planning') return snap();
    await screen.findByText(/Nothing has changed yet/);
    if (state === 'ready-to-review') return snap();
    await press('Add 1 runner');
    if (state === 'running') {
      await screen.findByText(/Registering/, { selector: 'p' });
      return snap();
    }
    await screen.findByText(/The runners are added/);
    return snap();
  },
};

// ---------------------------------------------------------------------------------------------------- Connect GitHub
const setupState = (hasApp: boolean) => ({
  has_app: hasApp,
  has_owner: false,
  setup_link_redeemed: true,
  ...(hasApp
    ? {
        app: {
          id: 1,
          slug: 'crsp-home',
          owner_login: 'octo',
          html_url: 'x',
          webhook_active: true,
          permissions: { actions: 'write' },
        },
      }
    : {}),
  steps: {
    connect_github: hasApp ? 'done' : 'todo',
    first_machine: 'todo',
    first_repository: 'todo',
  },
});

const connectDriver: WizardDriver = {
  id: 'connect-github',
  async open(state) {
    const api = signedIn()
      .on('GET', '/api/v1/setup', setupState(false))
      .on('POST', '/api/v1/setup/github-app', {
        post_url: 'https://github.example/settings/apps/new',
        manifest: {},
        state: 's',
      });
    let url = '/setup';
    const hasApp = state === 'done' || state === 'done-after-reload';
    if (state === 'planning') api.on('GET', '/api/v1/setup', never);
    if (state === 'running') api.on('POST', '/api/v1/setup/github-app', never);
    if (state === 'paused') url += '?app=started';
    if (state === 'stopped') url += '?app=cancelled';
    if (state === 'failed') url += '?app=failed';
    if (state === 'done') url += '?app=created';
    const client = createApiClient('http://controller.test', api.fetch as typeof fetch);
    const element = (loaded: boolean) => (
      <MemoryRouter initialEntries={[url]}>
        <ApiProvider client={client}>
          <ClockProvider>
            <SessionProvider>
              <ConnectGithubWizard
                {...(loaded ? { state: setupState(hasApp) as never } : {})}
                onDone={() => undefined}
              />
            </SessionProvider>
          </ClockProvider>
        </ApiProvider>
      </MemoryRouter>
    );
    const view = render(element(false));
    await step('Connect GitHub');
    const snap = () => read(state, 'Connect GitHub', false);
    if (state === 'loading') return snap();
    view.rerender(element(true));
    if (['paused', 'stopped', 'failed', 'done', 'done-after-reload'].includes(state)) {
      await screen.findByRole('button', { name: STORED });
      return snap();
    }
    await screen.findByRole('textbox', { name: 'App name' });
    if (state === 'first-run') return snap();
    await press('Next');
    if (state === 'planning') return snap();
    await screen.findByText(/Nothing has changed yet/);
    if (state === 'ready-to-review') return snap();
    await press('Create the App');
    return snap();
  },
};

// ---------------------------------------------------------------------------------------------------- Update
const upd = (extra: object = {}) => ({
  current_version: '1.4.0',
  latest_version: '1.5.0',
  update_available: true,
  state: 'idle',
  auto_update: false,
  running_jobs: [{ repository: 'octo/app', workflow: 'ci', run_id: 7, job: 'test' }],
  drain_timeout_minutes: 60,
  ...extra,
});

// A real hang: the body never ends, so the client waits (the shared `never` answers {} at once).
const hang = () => new Response(new ReadableStream());

const updateDriverFor = (compact: boolean): WizardDriver => ({
  id: compact ? 'update-page' : 'update',
  async open(state) {
    let applied = false;
    const api = signedIn()
      .on('GET', '/api/v1/update', () =>
        applied
          ? state === 'done'
            ? upd({ current_version: '1.5.0', update_available: false, running_jobs: [] })
            : upd({ state: 'waiting_for_jobs' })
          : upd(),
      )
      .on('POST', '/api/v1/update/apply', () => {
        applied = true;
        return upd({ state: 'waiting_for_jobs' });
      })
      .on('POST', '/api/v1/update/check', upd());
    let path = compact ? '/update' : '/settings/updates/review?kind=update';
    const sep = compact ? '?' : '&';
    const closeWord = compact ? 'Close' : 'Open Updates';
    const goWord = compact ? 'Update now' : 'Update to 1.5.0';
    if (state === 'loading') api.on('GET', '/api/v1/update', hang);
    if (state === 'first-run' || state === 'planning')
      api.on(
        'GET',
        '/api/v1/update',
        upd({ latest_version: undefined, update_available: false }),
      );
    if (state === 'planning') api.on('POST', '/api/v1/update/check', hang);
    if (state === 'paused') {
      path += `${sep}started=1&to=1.5.0`;
      api.on('GET', '/api/v1/update', upd({ state: 'waiting_for_jobs' }));
    }
    if (state === 'stopped')
      api.on(
        'GET',
        '/api/v1/update',
        upd({ latest_version: '1.4.0', update_available: false }),
      );
    if (state === 'failed') {
      // CRSP205: the page shows only the failure of its own attempt, named in the address.
      path += `${sep}attempt=a1`;
      api.on(
        'GET',
        '/api/v1/update',
        upd({ state: 'failed', error: 'the checksum check failed', error_attempt: 'a1' }),
      );
    }
    if (state === 'done-after-reload') {
      path += `${sep}started=1&to=1.5.0`;
      api.on(
        'GET',
        '/api/v1/update',
        upd({ current_version: '1.5.0', update_available: false, running_jobs: [] }),
      );
    }
    renderApp(api, path);
    await step('Update');
    const snap = () => read(state, 'Update', false);
    if (state === 'loading') return snap();
    if (['paused', 'stopped', 'failed', 'done-after-reload'].includes(state)) {
      await screen.findAllByRole('button', {
        name: new RegExp(`Start again|Try again|${closeWord}`),
      });
      return snap();
    }
    if (state === 'first-run' || state === 'planning') {
      await press('Look for updates');
      return snap();
    }
    await screen.findByText(/Nothing has changed yet/);
    if (state === 'ready-to-review') return snap();
    await press(goWord);
    if (state === 'running') {
      await screen.findByText(/Waiting for running jobs to end/, { selector: 'p' });
      return snap();
    }
    await screen.findByRole('button', { name: closeWord });
    return snap();
  },
});
const updateDriver = updateDriverFor(false);
const updatePageDriver = updateDriverFor(true);

describe('wizard harness (UX-5, UX-30, UX-54, R5.19)', () => {
  const drivers = [
    connectDriver,
    machineDriver,
    poolDriver,
    runnersDriver,
    updateDriver,
    updatePageDriver,
  ];
  for (const d of drivers) {
    it(`${d.id} passes the ten states and keeps its known list empty`, async () => {
      const lines: string[] = [];
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
        const one: WizardDriver = { id: d.id, open: () => d.open(state) };
        // judgeAll runs a driver through every state; here each state is opened on its own so one failure names its state.
        const snap = await d.open(state);
        expect(snap.state).toBe(state);
        void one;
        lines.push(`${d.id}:${state}`);
      }
      expect(lines).toHaveLength(10);
      cleanup();
      const problems = await judgeAll([
        {
          id: d.id,
          open: async (s) => {
            cleanup();
            return d.open(s);
          },
        },
      ]);
      expect(problems.join('\n')).toBe('');
    }, 5_000); // measured: slowest wizard 0.7 s
  }

  it('a failed run offers Try again and says what failed (UX-30)', async () => {
    await poolDriver.open('failed');
    expect(screen.getByText(/1 step failed/)).toBeTruthy();
    expect(screen.getByText('failed: GitHub refused the variable.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('Stop withdraws the join code and the wizard still ends at Done (R5.17)', async () => {
    const api = machineApi();
    renderApp(api, '/setup/machines/add');
    await screen.findByRole('radio', { name: 'Linux' });
    await press('Next');
    await press('Join a machine');
    await screen.findByText(/Run this command on the machine now/);
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    await screen.findByText(/Stopped\. The join code is withdrawn/);
    expect(api.count('DELETE', '/api/v1/join-codes/j1')).toBe(1);
    expect(screen.getByRole('button', { name: 'Start again' })).toBeTruthy();
  });

  // web-console 11, UX-30: Stop on an applying plan. The step that runs finishes, the plan ends `stopped`,
  // and the wizard still ends at Done saying what was done and what is left.
  for (const area of ['repository', 'runner'] as const) {
    it(`Stop on a ${area} plan that is applying ends at Done with what is left`, async () => {
      const repo = area === 'repository';
      const prefix = repo ? '/api/v1/repository-plans' : '/api/v1/runner-plans';
      let stopped = false;
      const steps = (state: string) => [
        {
          id: 's1',
          action: repo ? 'pool_on' : 'add_runner',
          text: 'First step',
          needs_yes: false,
          state: 'done',
        },
        {
          id: 's2',
          action: repo ? 'write_variable' : 'add_runner',
          text: 'Second step',
          needs_yes: false,
          state: state === 'stopped' ? 'skipped' : 'planned',
        },
      ];
      const mk = (state: string) => ({
        ...(repo ? plan(state) : rplan(state)),
        steps: steps(state),
      });
      const api = signedIn()
        .on('GET', '/api/v1/machines', { items: [machine()] })
        .on('GET', '/api/v1/github/repositories', {
          items: [repository({ pool_on: !repo })],
          stale: { stale: false },
        })
        .on('POST', prefix, mk('planned'))
        .on('POST', `${prefix}/:id/apply`, mk('applying'))
        .on('GET', `${prefix}/:id`, () => (stopped ? mk('stopped') : mk('applying')))
        .on('POST', `${prefix}/:id/stop`, () => {
          stopped = true;
          return mk('applying');
        });
      renderApp(api, repo ? '/setup/pool/turn-on' : '/setup/runners/add');
      if (repo) await tickOnce('Select octo/app');
      else await screen.findByRole('textbox', { name: /Labels/ });
      await press('Next');
      await screen.findByText(/Nothing has changed yet/);
      await press(repo ? 'Turn on 1 repository' : 'Add 1 runner');
      fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
      await screen.findByText(/Stopped\. 1 steps were done and 1 are left/);
      expect(api.count('POST', `${prefix}/${repo ? 'p1' : 'rp1'}/stop`)).toBe(1);
      expect(screen.getByRole('button', { name: 'Start again' })).toBeTruthy();
      expect(screen.getByText('First step')).toBeTruthy();
    });
  }

  it('Review counts again after a change to an earlier step', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines', { items: [machine()] })
      .on('GET', '/api/v1/github/repositories', {
        items: [repository({ pool_on: true })],
        stale: { stale: false },
      })
      .on('POST', '/api/v1/runner-plans', (c) => ({
        ...rplan('planned'),
        created_by: String((c.body as { count: number }).count),
      }));
    renderApp(api, '/setup/runners/add');
    await screen.findByRole('textbox', { name: /Labels/ });
    await press('Next');
    await screen.findByText(/1 persistent runner on build-1/);
    fireEvent.click(screen.getByRole('button', { name: /Runners$/ }));
    await press('Next');
    await waitFor(() => expect(api.count('POST', '/api/v1/runner-plans')).toBe(2));
  });

  it('a later step cannot be pressed and the busy primary says it is running', async () => {
    const snap = await poolDriver.open('first-run');
    expect(
      snap.steps.filter((s) => s.standing === 'later').every((s) => !s.pressable),
    ).toBe(true);
    cleanup();
    const run = await poolDriver.open('running');
    const primary = run.buttons.find((b) => b.primary);
    expect(primary?.disabled).toBe(true);
    expect(primary?.requestOpen).toBe(true);
    expect(primary?.label).toBe('Turning on');
  });
});
