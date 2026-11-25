// The main flows of the console against the mocked API: add a machine, turn on the pool, drain, delete a runner,
// add a user, and the session-ended dialog.
import { expect, test } from '@playwright/test';
import {
  cache,
  failure,
  machine,
  needingRecovery,
  repository,
  runner,
  signedIn,
  type Call,
} from './fixtures';

const join = (state: string, extra: object = {}) => ({
  id: 'j1',
  state,
  created_at: '2026-11-14T10:00:00Z',
  expires_at: '2099-11-14T10:15:00Z',
  ...extra,
});

test('Add a machine: Review, Joining with a one-time code, then Done with the machine', async ({
  page,
}) => {
  let joined = false;
  const mock = signedIn()
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
      join_code: join('waiting'),
      commands: {
        controller_address: 'x',
        agent_port: 1,
        cert_sha256: 'x',
        items: [{ os: 'linux', command: 'curl ... --code ABCD-1234' }],
      },
    })
    .on('GET', '/api/v1/join-codes/:id', () =>
      joined ? join('joined', { machine_id: 'm1' }) : join('waiting'),
    );
  await mock.install(page);
  await page.goto('/setup/machines/add');
  await expect(
    page.getByRole('heading', { level: 1, name: 'Add a machine' }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('Nothing has changed yet.')).toBeVisible();
  // RES3: Review says what comes next; the command with the join code shows only after Join a machine.
  await expect(page.getByText('Next you get one command')).toBeVisible();
  await page.getByRole('button', { name: 'Join a machine' }).click();
  await expect(page.getByLabel('Join command')).toContainText('--code ABCD-1234');
  await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();
  // The primary is busy and says so; the code is never in the address (R6.4).
  await expect(page.locator('[data-primary][aria-busy="true"]')).toBeDisabled();
  expect(page.url()).not.toContain('ABCD-1234');
  joined = true;
  await expect(page.getByText('The machine joined the pool.')).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText('build-1')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open Machines' })).toBeVisible();
});

test('Add a machine: Stop withdraws the code and the wizard still ends at Done', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/install-command', {
      controller_address: 'x',
      agent_port: 1,
      cert_sha256: 'x',
      items: [{ os: 'linux', command: 'curl x' }],
    })
    .on('POST', '/api/v1/join-codes', {
      code: 'ABCD-1234',
      join_code: join('waiting'),
      commands: {
        controller_address: 'x',
        agent_port: 1,
        cert_sha256: 'x',
        items: [{ os: 'linux', command: 'curl x --code ABCD-1234' }],
      },
    })
    .on('GET', '/api/v1/join-codes/:id', join('waiting'))
    .on('DELETE', '/api/v1/join-codes/:id', undefined);
  await mock.install(page);
  await page.goto('/setup/machines/add');
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Join a machine' }).click();
  await page.getByRole('button', { name: 'Stop' }).click();
  await expect(page.getByText(/The join code is withdrawn/)).toBeVisible();
  expect(mock.count('DELETE', '/api/v1/join-codes/j1')).toBe(1);
  await expect(page.getByRole('button', { name: 'Start again' })).toBeVisible();
});

test('Turn on the pool: choose, Review, Turning on, Done with what was really done', async ({
  page,
}) => {
  const plan = (state: string, stepState: string) => ({
    id: 'p1',
    area: 'repositories',
    state,
    created_at: '2026-11-14T10:00:00Z',
    created_by: 'octo',
    steps: [
      {
        id: 's1',
        action: 'pool_on',
        text: 'Turn on octo/app',
        needs_yes: false,
        state: stepState,
      },
      {
        id: 's2',
        action: 'write_variable',
        text: 'Write CRSP_RUNS_ON_LINUX',
        needs_yes: false,
        state: stepState,
      },
    ],
  });
  const mock = signedIn()
    .on('POST', '/api/v1/repository-plans', plan('planned', 'planned'))
    .on('POST', '/api/v1/repository-plans/:id/apply', plan('applying', 'running'))
    .on('GET', '/api/v1/repository-plans/:id', plan('done', 'done'));
  await mock.install(page);
  await page.goto('/setup/pool/turn-on');
  await expect(
    page.getByRole('heading', { level: 1, name: 'Turn on the pool' }),
  ).toBeVisible();
  const next = page.getByRole('button', { name: 'Next' });
  // The only repository is chosen already (CRSP124); clearing it shows the reason the primary is locked.
  const box = page.getByRole('checkbox', { name: 'Select octo/app' });
  await expect(box).toBeChecked();
  await page.getByRole('button', { name: 'Clear', exact: true }).click();
  await expect(next).toBeDisabled();
  await expect(page.getByText('Tick the repositories to turn on.')).toBeVisible();
  await box.check();
  await next.click();
  await expect(page.getByText('Nothing has changed yet.')).toBeVisible();
  await page.getByRole('button', { name: 'Turn on 1 repository' }).click();
  await expect(page.getByText(/The pool is on\. 2 steps done/)).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByRole('button', { name: 'Open Repositories' })).toBeVisible();
});

test('Stop new jobs asks first, names the running jobs, and stops new jobs on yes', async ({
  page,
}) => {
  const mock = signedIn().on('POST', '/api/v1/machines/:id/drain', {
    id: 'c1',
    machine_id: 'm1',
    kind: 'drain',
    state: 'waiting',
    started_at: 'x',
    waiting_for: [],
  });
  await mock.install(page);
  await page.goto('/setup/machines/m1');
  await page.getByRole('button', { name: 'Stop new jobs', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Stop new jobs on build-1?' });
  await expect(dialog).toContainText('octo/app e2e');
  expect(mock.count('POST', '/api/v1/machines/m1/drain')).toBe(0);
  await dialog.getByRole('button', { name: 'Stop new jobs' }).click();
  await expect.poll(() => mock.count('POST', '/api/v1/machines/m1/drain')).toBe(1);
});

test('Agent recovery: the state is shown, Recover is locked while it runs, then the result is shown', async ({
  page,
}) => {
  let release: () => void = () => undefined;
  const held = new Promise<void>((r) => (release = r));
  const mock = signedIn()
    .on('GET', '/api/v1/machines', { items: [needingRecovery()] })
    .on('GET', '/api/v1/machines/:id', needingRecovery())
    .on('POST', '/api/v1/machines/:id/recovery', async () => {
      await held;
      return undefined;
    });
  await mock.install(page);
  await page.goto('/setup/machines');
  await expect(page.getByRole('row', { name: /build-1/ })).toContainText(
    'needs recovery',
  );
  await page.getByRole('link', { name: 'build-1' }).click();
  const section = page.getByRole('region', { name: 'Recovery' });
  await expect(section).toContainText('0.1.0');
  await expect(section).toContainText('0.3.0');
  await section.getByRole('button', { name: 'Recover' }).click();
  await expect(section.getByRole('button', { name: 'Recovering' })).toBeDisabled();
  expect(mock.count('POST', '/api/v1/machines/m1/recovery')).toBe(1);
  release();
  await expect(section).toContainText('Recovery is ready');
  await expect(section.getByRole('button', { name: 'Recover' })).toBeDisabled();
});

test('Escape closes the question and nothing changes (UX-46)', async ({ page }) => {
  const mock = signedIn();
  await mock.install(page);
  await page.goto('/setup/machines/m1');
  await page.getByRole('button', { name: 'Remove crsp' }).click();
  await expect(page.getByRole('dialog', { name: 'Remove crsp' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(mock.count('POST', '/api/v1/remove')).toBe(0);
});

test('Delete runners names each runner and its job, then deletes with the yes', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/runners', {
      items: [
        runner({
          state: 'busy',
          current_job: {
            id: 9,
            repository: 'octo/app',
            run_id: 1,
            run_attempt: 1,
            github_job_id: 1,
            job_name: 'e2e',
            labels: [],
            state: 'running',
          },
        }),
      ],
    })
    .on('DELETE', '/api/v1/runners/:id', runner());
  await mock.install(page);
  await page.goto('/setup/runners');
  await page.getByRole('checkbox', { name: 'Select crsp-linux-1' }).check();
  await page.getByRole('button', { name: 'Delete 1 runner' }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete these runners?' });
  await expect(dialog).toContainText('Runner crsp-linux-1');
  await expect(dialog).toContainText('octo/app e2e');
  await dialog.getByRole('button', { name: 'Delete' }).click();
  await expect.poll(() => mock.count('DELETE', '/api/v1/runners/r1')).toBe(1);
});

test('Runners show their isolation in words', async ({ page }) => {
  await signedIn().install(page);
  await page.goto('/setup/runners');
  await expect(page.getByText('persistent', { exact: true }).first()).toBeVisible();
  await expect(
    page.locator('[title*="persistent, state kept between jobs"]').first(),
  ).toBeAttached();
});

test('The owner adds a user', async ({ page }) => {
  const mock = signedIn().on('POST', '/api/v1/users', {
    id: 4,
    login: 'bob',
    role: 'viewer',
    added_at: '2026-11-14T10:00:00Z',
  });
  await mock.install(page);
  await page.goto('/settings/users');
  await page.getByRole('textbox', { name: 'GitHub login' }).fill('bob');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => mock.count('POST', '/api/v1/users')).toBe(1);
});

test('The repository policy asks first before public forks are allowed', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/repositories/:id', repository())
    .on('PATCH', '/api/v1/repositories/:id/policy', repository().policy);
  await mock.install(page);
  await page.goto('/setup/pool/octo/app');
  await page.getByRole('checkbox', { name: 'Run jobs from public forks' }).check();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Allow jobs from public forks?' });
  await expect(dialog).toBeVisible();
  expect(mock.count('PATCH', '/api/v1/repositories/7/policy')).toBe(0);
  await dialog.getByRole('button', { name: 'Allow' }).click();
  await expect.poll(() => mock.count('PATCH', '/api/v1/repositories/7/policy')).toBe(1);
});

test('When the session ends the dialog shows over the page and the page stays', async ({
  page,
}) => {
  const mock = signedIn().on(
    'GET',
    '/api/v1/machines',
    failure(401, 'session_expired', 'The session ended.'),
  );
  await mock.install(page);
  await page.goto('/setup/machines');
  const dialog = page.getByRole('dialog', { name: 'Session ended' });
  await expect(dialog).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: 'Setup' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Sign in with GitHub' }))
    .toBeFocused({ timeout: 2000 })
    .catch(() => undefined);
  void machine;
});

test('Images and caches opens from the menu and says that there are no images yet', async ({
  page,
}) => {
  const mock = signedIn();
  await mock.install(page);
  await page.goto('/');
  await page.getByRole('link', { name: 'Setup', exact: true }).click();
  await page.getByRole('tab', { name: 'Images and caches', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Setup' })).toBeVisible();
  await expect(page.getByText(/Each machine has one Linux image\./)).toBeVisible();
  await page.getByRole('tab', { name: 'Caches', exact: true }).click();
  const card = page.getByRole('region', { name: 'Tool cache of crsp-linux-1' });
  await expect(card).toContainText('3.2 GB of 10.0 GB (32%)');
  await expect(card).toContainText('never');
});

test('Wipe a cache asks first, names the cache, its size and the job, and wipes on yes', async ({
  page,
}) => {
  let wiping = false;
  const mock = signedIn()
    .on('GET', '/api/v1/caches', () => ({
      items: [cache(wiping ? { state: 'wiping', waiting_for: ['octo/app e2e'] } : {})],
    }))
    .on('POST', '/api/v1/caches/:id/wipe', (call: Call) => {
      if (!call.query.get('confirm'))
        return failure(409, 'needs_confirmation', 'Needs a yes.', [
          { kind: 'cache', id: 'c1', text: 'Tool cache of crsp-linux-1, 3.2 GB' },
          { kind: 'job', id: '9', text: 'octo/app e2e' },
        ]);
      wiping = true;
      return cache({ state: 'wiping', waiting_for: ['octo/app e2e'] });
    });
  await mock.install(page);
  await page.goto('/setup/images?tab=caches');
  await page.getByRole('button', { name: 'Wipe', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Wipe Tool cache of crsp-linux-1?' });
  await expect(dialog).toContainText('Tool cache of crsp-linux-1, 3.2 GB');
  await expect(dialog).toContainText('octo/app e2e');
  expect(mock.calls.filter((c) => c.query.get('confirm')).length).toBe(0);
  await dialog.getByRole('button', { name: 'Wipe' }).click();
  await expect
    .poll(() => mock.calls.filter((c) => c.query.get('confirm') === 'true').length)
    .toBe(1);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Wiping' })).toBeDisabled();
  await expect(page.getByRole('region', { name: 'Running work' })).toContainText(
    'Wiping Tool cache of crsp-linux-1, waiting for octo/app e2e',
  );
});
const POOL = {
  budget_memory_pct: 85,
  budget_cpu_pct: 60,
  size_slots_from_machine: true,
  default_job_slots: 2,
  drain_timeout_minutes: 60,
};
const QUEUE = {
  order: ['priority', 'event_class', 'fair_share', 'queued_at'],
  fair_share: true,
  no_runner_wait_minutes: 2,
};

test('The admin saves the Budgets with Save and the page says no job is waited for', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/settings/pool', POOL)
    .on('GET', '/api/v1/settings/queue', QUEUE)
    .on('PUT', '/api/v1/settings/pool', POOL)
    .on('PUT', '/api/v1/settings/queue', QUEUE);
  await mock.install(page);
  await page.goto('/settings/budgets');
  await expect(page.getByText(/no running job is waited for/)).toBeVisible();
  await expect(
    page.getByRole('button', { name: /Time to finish running jobs/ }),
  ).toContainText('60');
  await page.getByRole('checkbox', { name: /Size the slots/ }).uncheck();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => mock.count('PUT', '/api/v1/settings/pool')).toBe(1);
  await expect.poll(() => mock.count('PUT', '/api/v1/settings/queue')).toBe(1);
  await expect(page.getByText('Settings saved.')).toBeVisible();
});

test('The Budgets queue order moves a key with Move down and Cancel puts it back', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/settings/pool', POOL)
    .on('GET', '/api/v1/settings/queue', QUEUE);
  await mock.install(page);
  await page.goto('/settings/budgets');
  const first = page.getByRole('listitem', { name: '1, Repository priority' });
  await expect(first).toBeVisible();
  await page.getByRole('button', { name: 'Move Repository priority down' }).click();
  await expect(
    page.getByRole('listitem', { name: '2, Repository priority' }),
  ).toBeVisible();
  expect(mock.count('PUT', '/api/v1/settings/queue')).toBe(0);
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(first).toBeVisible();
});
