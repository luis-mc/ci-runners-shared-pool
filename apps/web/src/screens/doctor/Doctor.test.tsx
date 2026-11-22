// R5.26: findings grouped by area with a status word (UX-58), what is wrong and its fix, the time of the last run
// (UX-53), and "Run checks again" as the one primary, busy with progress while the run lasts (UX-13, UX-29).
// R5.31: loading, empty, could not load.
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import {
  FakeEventSource,
  apiError,
  renderApp,
  signedIn,
  viewer,
} from '../../testing/mock';
import { areaOf, groupFindings, severityStatus } from './Doctor';

afterEach(cleanup);

type Finding = Schemas['DoctorFinding'];

function finding(over: Partial<Finding> = {}): Finding {
  return {
    check: 'workflow.labels',
    subject: { kind: 'repository', id: '7', name: 'octo/app' },
    severity: 'fail',
    title: 'No online runner carries [self-hosted, linux, arm64]',
    detail: '.github/workflows/ci.yml:12 needs [self-hosted, linux, arm64]',
    fix: { text: 'Add a runner with these labels.', link: '/setup/runners' },
    needs_admin: false,
    milestone: 1,
    checked_at: '2026-11-14T10:00:12Z',
    ...over,
  };
}

const lastRun = new Date(Date.now() - 5 * 60_000).toISOString();

function run(over: Partial<Schemas['DoctorRun']> = {}): Schemas['DoctorRun'] {
  return {
    id: 'run1',
    trigger: 'web',
    state: 'running',
    started_at: lastRun,
    counts: {},
    findings: [],
    ...over,
  };
}

describe('doctor words', () => {
  it('names the area of a check by its first part', () => {
    expect(areaOf('machine.online')).toBe('Machines');
    expect(areaOf('github.app')).toBe('GitHub registration');
    expect(areaOf('repo.vars')).toBe('Repository variables');
    expect(areaOf('cache.mirror')).toBe('Cache');
  });

  it('gives every severity a word, a glyph kind and a colour (UX-58)', () => {
    expect(severityStatus('fail')).toEqual({ kind: 'failed', word: 'failed' });
    expect(severityStatus('warn')).toEqual({ kind: 'waiting', word: 'warning' });
    expect(severityStatus('ok').word).toBe('ok');
    expect(severityStatus('skip').word).toBe('skipped');
  });

  it('groups the findings by area in screen order and leaves out the ok ones', () => {
    const groups = groupFindings([
      finding(),
      finding({ check: 'machine.online', severity: 'warn' }),
      finding({ check: 'machine.clock', severity: 'ok' }),
      finding({ check: 'zzz.thing', severity: 'info' }),
    ]);
    expect(groups.map((g) => g.area)).toEqual(['Machines', 'Workflow labels', 'Zzz']);
    expect(groups[0]?.items.length).toBe(1);
  });
});

describe('the logon right finding (runners R5.89, doctor R5.45)', () => {
  const lost = finding({
    check: 'runner.logon',
    title: 'The account .\\crsp-a of crsp-a cannot log on as a service',
    subject: { kind: 'runner', id: 'r1', name: 'crsp-a' },
    fix: {
      text: 'Press Start. The agent gives the account the right again.',
      link: '/setup/machines/m9',
    },
  });

  it('has one Start button that asks the machine to give the right and start the service', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/doctor/findings', { items: [lost], last_run_at: lastRun })
      .on('POST', '/api/v1/machines/:id/redetect', { status: 202 });
    renderApp(api, '/doctor');
    fireEvent.click(await screen.findByRole('button', { name: 'Start' }));
    await waitFor(() =>
      expect(api.count('POST', '/api/v1/machines/m9/redetect')).toBe(1),
    );
  });

  it('has no Start button on other findings', async () => {
    const api = signedIn().on('GET', '/api/v1/doctor/findings', {
      items: [finding()],
      last_run_at: lastRun,
    });
    renderApp(api, '/doctor');
    await screen.findByText(/1 problem found/);
    expect(screen.queryByRole('button', { name: 'Start' })).toBeNull();
  });
});

describe('Doctor screen', () => {
  it('shows the loading state first', async () => {
    renderApp(signedIn().on('GET', '/api/v1/doctor/findings', { items: [] }), '/doctor');
    expect(await screen.findByText('Loading the doctor findings')).toBeTruthy();
  });

  it('lists findings by area with status word, what is wrong, the fix and the last run', async () => {
    const api = signedIn().on('GET', '/api/v1/doctor/findings', {
      items: [
        finding(),
        finding({
          check: 'machine.online',
          severity: 'warn',
          title: 'build-2 is offline',
          subject: { kind: 'machine', id: 'm2', name: 'build-2' },
          fix: { text: 'Start the agent.', command: 'crsp agent start' },
          needs_admin: true,
        }),
      ],
      last_run_at: lastRun,
    });
    renderApp(api, '/doctor');
    const machines = await screen.findByRole('region', { name: 'Machines' });
    expect(within(machines).getByText('warning')).toBeTruthy();
    expect(within(machines).getByText('build-2 is offline')).toBeTruthy();
    expect(within(machines).getByText('Start the agent.')).toBeTruthy();
    expect(within(machines).getByLabelText('Fix command').textContent).toBe(
      'crsp agent start',
    );
    const labels = screen.getByRole('region', { name: 'Workflow labels' });
    expect(within(labels).getByText('failed')).toBeTruthy();
    // LOOK19: the detail is one clipped line with a Details button; the full text opens in a row under it.
    const details = within(labels).getByRole('button', { name: 'Details' });
    expect(details.getAttribute('aria-expanded')).toBe('false');
    expect(within(labels).queryByRole('region', { name: /Details/ })).toBeNull();
    fireEvent.click(details);
    expect(details.getAttribute('aria-expanded')).toBe('true');
    expect(within(labels).getByRole('region', { name: /Details/ }).textContent).toBe(
      '.github/workflows/ci.yml:12 needs [self-hosted, linux, arm64]',
    );
    expect(details.getAttribute('aria-controls')).toBe(
      within(labels).getByRole('region', { name: /Details/ }).id,
    );
    expect(
      within(labels).getByRole('button', { name: 'Open' }).getAttribute('href'),
    ).toBe('/setup/runners');
    expect(
      screen.getByText(/Last run \d{4}-\d{2}-\d{2} \d{2}:\d{2} \(5 min ago\)\./),
    ).toBeTruthy();
    expect(screen.getByText(/2 problems found/)).toBeTruthy();
    expect(screen.queryByText('needs admin')).toBeNull(); // the owner is admin
  });

  it('says No problems found with the time of the run when every finding is ok', async () => {
    const api = signedIn().on('GET', '/api/v1/doctor/findings', {
      items: [finding({ severity: 'ok', title: 'fine' })],
      last_run_at: lastRun,
    });
    renderApp(api, '/doctor');
    expect(await screen.findByText(/No problems found\. The doctor ran/)).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });

  it('says no checks have run and offers to run them', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/doctor/findings', { items: [] })
      .on('POST', '/api/v1/doctor/runs', run({ state: 'done' }));
    renderApp(api, '/doctor');
    expect(
      await screen.findByText(
        'No checks have run yet. Use Run checks below to look for problems.',
      ),
    ).toBeTruthy();
    const buttons = screen.getAllByRole('button', { name: 'Run checks' });
    expect(buttons.length).toBe(1); // one primary, not a second one in the empty state
    expect(screen.getByRole('heading', { level: 1, name: 'Doctor' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Findings' })).toBeTruthy();
    fireEvent.click(buttons[0] as HTMLElement);
    await waitFor(() => expect(api.count('POST', '/api/v1/doctor/runs')).toBe(1));
  });

  it('says what failed when the findings could not load, and tries again (UX-28)', async () => {
    let n = 0;
    const api = signedIn().on('GET', '/api/v1/doctor/findings', () => {
      n += 1;
      return n === 1
        ? apiError(500, 'internal', 'the findings could not be read')
        : { items: [finding()], last_run_at: lastRun };
    });
    renderApp(api, '/doctor');
    expect(await screen.findByText('Could not load the doctor findings.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('region', { name: 'Workflow labels' })).toBeTruthy();
  });

  it('runs the doctor: busy with progress while it runs, findings reload when it ends (UX-13, UX-29)', async () => {
    let state: 'running' | 'done' = 'running';
    let findings: Finding[] = [];
    const api = signedIn()
      .on('GET', '/api/v1/doctor/findings', () => ({
        items: findings,
        last_run_at: lastRun,
      }))
      .on('POST', '/api/v1/doctor/runs', () => run({ state: 'running' }))
      .on('GET', '/api/v1/doctor/runs/:id', () => run({ state, counts: { fail: 1 } }));
    renderApp(api, '/doctor');
    await screen.findByText(/No problems found/);
    fireEvent.click(screen.getByRole('button', { name: 'Run checks again' }));
    const busy = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Running the doctor',
    });
    expect(busy.disabled).toBe(true);
    expect(screen.getByRole('progressbar', { name: 'Running the doctor' })).toBeTruthy();
    expect(
      await screen.findByText(/Running the doctor\. 1 check done so far\./),
    ).toBeTruthy();
    state = 'done';
    findings = [finding()];
    act(() =>
      FakeEventSource.latest().emit({ id: '2', type: 'doctor.progress', at: 'x' }),
    );
    expect(await screen.findByRole('region', { name: 'Workflow labels' })).toBeTruthy();
    const again = screen.getByRole<HTMLButtonElement>('button', {
      name: 'Run checks again',
    });
    expect(again.disabled).toBe(false);
    expect(screen.queryByRole('progressbar')).toBeNull();
  });

  it('says in one line when the run could not start', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/doctor/findings', { items: [finding()], last_run_at: lastRun })
      .on(
        'POST',
        '/api/v1/doctor/runs',
        apiError(500, 'internal', 'the doctor could not start'),
      );
    renderApp(api, '/doctor');
    await screen.findByRole('region', { name: 'Workflow labels' });
    fireEvent.click(screen.getByRole('button', { name: 'Run checks again' }));
    expect(
      await screen.findByText(/Could not run the doctor\. the doctor could not start/),
    ).toBeTruthy();
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Run checks again' })
        .disabled,
    ).toBe(false);
  });

  it('keeps Run checks again as the one primary, in the bar; a fix button in a row stays normal (D27)', async () => {
    const api = signedIn().on('GET', '/api/v1/doctor/findings', {
      items: [finding({ fix: { text: 'Open it.', link: '/setup/machines' } })],
      last_run_at: lastRun,
    });
    renderApp(api, '/doctor');
    const bar = await screen.findByRole('region', { name: 'Actions' });
    const again = await within(bar).findByRole('button', { name: 'Run checks again' });
    expect(again.getAttribute('data-primary')).toBe('true');
    const fix = screen.getByRole('button', { name: 'Open' });
    expect(fix.hasAttribute('data-primary')).toBe(false);
    expect(document.querySelectorAll('[data-primary]')).toHaveLength(1);
  });

  it('keeps a viewer from running it and says why (UX-38)', async () => {
    const api = signedIn(viewer).on('GET', '/api/v1/doctor/findings', {
      items: [finding({ needs_admin: true })],
      last_run_at: lastRun,
    });
    renderApp(api, '/doctor');
    const button = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Run checks again',
    });
    expect(button.disabled).toBe(true);
    expect(await screen.findByText('needs admin')).toBeTruthy();
  });
});

describe('runner leftovers finding (doctor R5.48, OPS33)', () => {
  const leftovers: Schemas['RunnerLeftover'][] = [
    {
      kind: 'service',
      name: 'actions.runner.old',
      account: '.\\crsp-old',
      why: 'No runner of this controller owns it.',
    },
    {
      kind: 'folder',
      name: 'C:\\runners\\old',
      size_bytes: 2_147_483_648,
      why: 'No runner record points to it.',
    },
  ];
  const leftoverFinding = (over: Partial<Finding> = {}): Finding => ({
    ...finding({
      check: 'machine.runner_leftovers',
      severity: 'warn',
      title: 'build-2 has runner leftovers',
      subject: { kind: 'machine', id: 'm2', name: 'build-2' },
      fix: { text: 'Remove the leftovers.' },
      items: leftovers,
      action: { id: 'remove_runner_leftovers', label: 'Remove' },
    }),
    detail: 'The machine has services and folders no runner owns.',
    ...over,
  });
  const REMOVE = '/api/v1/machines/m2/runner-leftovers/remove';
  const setup = (f: Finding = leftoverFinding(), who = signedIn()) =>
    who
      .on('GET', '/api/v1/doctor/findings', { items: [f], last_run_at: lastRun })
      .on('POST', '/api/v1/doctor/runs', run({ state: 'done' }));

  it('lists each leftover with kind, name, size, account and why', async () => {
    renderApp(setup(), '/doctor');
    const list = await screen.findByRole('list', { name: 'Runner leftovers' });
    const lines = within(list)
      .getAllByRole('listitem')
      .map((l) => l.textContent);
    expect(lines).toEqual([
      'Service actions.runner.old, account .\\crsp-old. No runner of this controller owns it.',
      'Folder C:\\runners\\old (2.1 GB). No runner record points to it.',
    ]);
  });

  it('asks first, naming every item, and does nothing on Cancel', async () => {
    const api = setup();
    renderApp(api, '/doctor');
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));
    const card = await screen.findByRole('dialog');
    expect(within(card).getByText(/cannot be undone/)).toBeTruthy();
    expect(within(card).getByText('Remove these leftovers from build-2?')).toBeTruthy();
    const items = within(within(card).getByRole('list', { name: 'What goes' }))
      .getAllByRole('listitem')
      .map((l) => l.textContent);
    expect(items).toEqual([
      'Service actions.runner.old',
      'Folder C:\\runners\\old (2.1 GB)',
    ]);
    expect(
      within(card).getByRole('button', { name: 'Remove' }).getAttribute('data-kind'),
    ).toBe('danger');
    fireEvent.click(within(card).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(api.count('POST', REMOVE)).toBe(0);
  });

  it('removes exactly the items, shows the result per item and runs the check again for the machine', async () => {
    let release: (() => void) | undefined;
    const gate = new Promise<void>((r) => (release = r));
    const api = setup().on('POST', REMOVE, async () => {
      await gate;
      return {
        results: [
          {
            kind: 'service',
            name: 'actions.runner.old',
            outcome: 'removed',
            steps: ['stopped', 'deleted'],
          },
          {
            kind: 'folder',
            name: 'C:\\runners\\old',
            outcome: 'failed',
            error: 'A file is in use.',
            steps: [],
          },
        ],
      };
    });
    renderApp(api, '/doctor');
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));
    const card = await screen.findByRole('dialog');
    fireEvent.click(within(card).getByRole('button', { name: 'Remove' }));
    const busy = await within(
      await screen.findByRole('dialog'),
    ).findByRole<HTMLButtonElement>('button', { name: 'Removing' });
    expect(busy.disabled).toBe(true);
    release?.();
    const results = await screen.findByRole('list', { name: 'Removal results' });
    expect(
      within(results)
        .getAllByRole('listitem')
        .map((l) => l.textContent),
    ).toEqual([
      'Service actions.runner.old: Removed',
      'Folder C:\\runners\\old: Failed. A file is in use.',
    ]);
    expect(api.calls.find((c) => c.path === REMOVE)?.body).toEqual({
      items: [
        { kind: 'service', name: 'actions.runner.old' },
        { kind: 'folder', name: 'C:\\runners\\old' },
      ],
    });
    await waitFor(() =>
      expect(api.calls.find((c) => c.path === '/api/v1/doctor/runs')?.body).toEqual({
        scope: { machines: ['m2'], checks: ['machine.runner_leftovers'] },
      }),
    );
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('disables Remove and says why when the agent has no administrator rights', async () => {
    renderApp(setup(leftoverFinding({ needs_admin: true })), '/doctor');
    const button = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Remove',
    });
    expect(button.disabled).toBe(true);
    expect(
      screen.getByText(
        'The agent has no administrator rights. Run it as a service with rights.',
      ),
    ).toBeTruthy();
  });

  it('keeps a viewer from removing and says why (UX-38)', async () => {
    renderApp(setup(leftoverFinding(), signedIn(viewer)), '/doctor');
    const button = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Remove',
    });
    expect(button.disabled).toBe(true);
  });

  it('has no Remove on a finding without the action', async () => {
    const { action: _action, ...bare } = leftoverFinding();
    renderApp(setup(bare), '/doctor');
    await screen.findByRole('list', { name: 'Runner leftovers' });
    expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();
  });

  describe('half-registered runners (doctor R5.50)', () => {
    const REG = '/api/v1/doctor/registration-fixes';
    const stray = (): Finding =>
      finding({
        check: 'runner.orphan',
        severity: 'warn',
        title: 'crsp-stray on GitHub belongs to no machine',
        subject: { kind: 'github_runner', id: '555', name: 'crsp-stray' },
        fix: { text: 'Press Remove.' },
        action: {
          id: 'remove_github_runner',
          label: 'Remove',
          args: {
            github_runner_id: 555,
            name: 'crsp-stray',
            installation_id: 7,
            owner: 'o',
            repo: 'r',
          },
        },
      });
    it('asks first and sends the args of the finding with a yes', async () => {
      const api = signedIn()
        .on('GET', '/api/v1/doctor/findings', { items: [stray()], last_run_at: lastRun })
        .on('POST', '/api/v1/doctor/runs', run({ state: 'done' }))
        .on('POST', REG, { removed: true, name: 'crsp-stray' });
      renderApp(api, '/doctor');
      fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));
      const card = await screen.findByRole('dialog');
      expect(within(card).getByText('Remove crsp-stray from GitHub?')).toBeTruthy();
      expect(api.count('POST', REG)).toBe(0);
      fireEvent.click(within(card).getByRole('button', { name: 'Remove' }));
      await waitFor(() => expect(api.count('POST', REG)).toBe(1));
    });
  });
});
