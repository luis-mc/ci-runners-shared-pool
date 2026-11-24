// CRSP37 part C against the mocked API: the audit log export, the config export and Stop on an applying plan
// (web-console 11, UX-30, UX-39, R5.29).
import { expect, test } from '@playwright/test';
import { machine, repository, signedIn, viewer } from './fixtures';

const event = (id: number, action: string) => ({
  id,
  at: '2026-11-14T10:00:00Z',
  actor: 'octo',
  source: 'web',
  action,
  target_type: 'machine',
  target_id: 'm1',
  summary: `did ${action}`,
  result: 'done',
});

test('Audit log: Export saves a file named for what it holds, then the stamp (UX-39)', async ({
  page,
}) => {
  const mock = signedIn().on('GET', '/api/v1/audit', {
    items: [event(2, 'machine.drain'), event(1, 'settings.update')],
  });
  await mock.install(page);
  await page.route('**/api/v1/exports/audit*', (route) =>
    route.fulfill({
      status: 200,
      headers: {
        'content-type': 'text/csv',
        'content-disposition': 'attachment; filename="crsp-audit-2026-11-14-1005.csv"',
      },
      body: 'id,at\n2,x\n1,x\n',
    }),
  );
  await page.goto('/settings/audit');
  await expect(page.getByRole('table', { name: 'Audit log' })).toBeVisible();
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export' }).click();
  expect((await download).suggestedFilename()).toBe('crsp-audit-2026-11-14-1005.csv');
});

test('Config export: an admin downloads crsp-config-<stamp>.yaml; a viewer sees the reason', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.route('**/api/v1/exports/config', (route) =>
    route.fulfill({
      status: 200,
      headers: {
        'content-type': 'application/yaml',
        'content-disposition': 'attachment; filename="crsp-config-2026-11-14-1005.yaml"',
      },
      body: 'product: crsp\n',
    }),
  );
  await page.goto('/settings/export');
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export config' }).click();
  expect((await download).suggestedFilename()).toBe('crsp-config-2026-11-14-1005.yaml');
});

test('Config export: a viewer cannot export', async ({ page }) => {
  await signedIn(viewer).install(page);
  await page.goto('/settings/export');
  await expect(page.getByRole('button', { name: 'Export config' })).toBeDisabled();
});

const steps = (stopped: boolean) => [
  {
    id: 's1',
    action: 'pool_on',
    text: 'Turn on octo/app',
    needs_yes: false,
    state: 'done',
  },
  {
    id: 's2',
    action: 'write_variable',
    text: 'Write CRSP_RUNS_ON_LINUX',
    needs_yes: false,
    state: stopped ? 'skipped' : 'planned',
  },
];

test('Turn on the pool: Stop ends the plan stopped and Done says what is left', async ({
  page,
}) => {
  let stopped = false;
  const plan = (state: string) => ({
    id: 'p1',
    area: 'repositories',
    state,
    created_at: '2026-11-14T10:00:00Z',
    created_by: 'octo',
    steps: steps(stopped),
  });
  const mock = signedIn()
    .on('POST', '/api/v1/repository-plans', plan('planned'))
    .on('POST', '/api/v1/repository-plans/:id/apply', plan('applying'))
    .on('GET', '/api/v1/repository-plans/:id', () =>
      plan(stopped ? 'stopped' : 'applying'),
    )
    .on('POST', '/api/v1/repository-plans/:id/stop', () => {
      stopped = true;
      return plan('applying');
    });
  await mock.install(page);
  await page.goto('/setup/pool/turn-on');
  await page.getByRole('checkbox', { name: 'Select octo/app' }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Turn on 1 repository' }).click();
  // Later steps cannot be pressed while the work runs, and Stop is there.
  await expect(page.locator('[data-primary][aria-busy="true"]')).toBeDisabled();
  await page.getByRole('button', { name: 'Stop' }).click();
  await expect(page.getByText(/Stopped\. 1 steps were done and 1 are left/)).toBeVisible({
    timeout: 10_000,
  });
  expect(mock.count('POST', '/api/v1/repository-plans/p1/stop')).toBe(1);
  await expect(page.getByRole('button', { name: 'Start again' })).toBeVisible();
});

test('Add runners: Stop ends the plan stopped and Done says what is left', async ({
  page,
}) => {
  let stopped = false;
  const plan = (state: string) => ({
    id: 'rp1',
    area: 'runners',
    state,
    created_at: '2026-11-14T10:00:00Z',
    created_by: 'octo',
    steps: steps(stopped).map((s) => ({ ...s, action: 'add_runner' })),
  });
  const mock = signedIn()
    .on('GET', '/api/v1/github/repositories', {
      items: [
        {
          id: 7,
          full_name: 'octo/app',
          owner: 'octo',
          name: 'app',
          installation_id: 1,
          visibility: 'private',
          pool_on: true,
          user_is_github_admin: true,
          user_admin_known: true,
          variables: [],
          policy: {
            priority: 0,
            max_parallel_jobs: 2,
            allow_fork_pull_requests: false,
            labels: ['self-hosted'],
            runner_groups: [],
          },
        },
      ],
      stale: { stale: false },
    })
    .on('POST', '/api/v1/runner-plans', plan('planned'))
    .on('POST', '/api/v1/runner-plans/:id/apply', plan('applying'))
    .on('GET', '/api/v1/runner-plans/:id', () => plan(stopped ? 'stopped' : 'applying'))
    .on('POST', '/api/v1/runner-plans/:id/stop', () => {
      stopped = true;
      return plan('applying');
    });
  await mock.install(page);
  await page.goto('/setup/runners/add');
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Add 1 runner' }).click();
  await page.getByRole('button', { name: 'Stop' }).click();
  await expect(page.getByText(/Stopped\. 1 steps were done and 1 are left/)).toBeVisible({
    timeout: 10_000,
  });
  expect(mock.count('POST', '/api/v1/runner-plans/rp1/stop')).toBe(1);
});

// OPS27: Linux runners on a Windows machine that runs Linux containers.
const winMachine = (docker: boolean) =>
  machine({
    id: 'm2',
    name: 'GameRoomPC',
    os: 'windows',
    container_os: 'linux',
    backends: [
      { name: 'persistent', present: true, usable: true, allowed: true },
      { name: 'docker', present: true, usable: true, allowed: docker },
    ],
  });
const emptyRepos = { items: [], stale: { stale: false } };
const poolPlan = (state: string, stepState: string) => ({
  id: 'rp9',
  area: 'runners',
  state,
  created_at: '2026-11-18T10:00:00Z',
  created_by: 'octo',
  steps: [
    {
      id: 's1',
      action: 'add_runner',
      text: 'Add ephemeral runner crsp-pool-1 on machine GameRoomPC',
      needs_yes: false,
      state: stepState,
    },
  ],
});

test('Add runners: Linux on a Windows machine is planned and added to the pool', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/machines', { items: [winMachine(true)] })
    .on('GET', '/api/v1/github/repositories', emptyRepos)
    .on('POST', '/api/v1/runner-plans', poolPlan('planned', 'planned'))
    .on('POST', '/api/v1/runner-plans/:id/apply', poolPlan('applying', 'running'))
    .on('GET', '/api/v1/runner-plans/:id', poolPlan('done', 'done'));
  await mock.install(page);
  await page.goto('/setup/runners/add');
  await page.getByRole('radio', { name: /^Ephemeral/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('Add ephemeral runner crsp-pool-1')).toBeVisible();
  await page.getByRole('button', { name: 'Allow 1 ephemeral runner' }).click();
  await expect(page.getByText('The runners are added.')).toBeVisible({ timeout: 10_000 });
  expect(mock.count('POST', '/api/v1/runner-plans')).toBe(1);
});

// OPS60 (runners R5.100, R5.101; core UX-30): a runner whose image build failed never ends at "The runners are added".
test('Add runners: a failed runner ends at Done with its reason and a next step', async ({
  page,
}) => {
  const failedPlan = {
    ...poolPlan('failed', 'failed'),
    steps: [{ ...poolPlan('failed', 'failed').steps[0], error: 'build failed (exit 1)' }],
  };
  const mock = signedIn()
    .on('GET', '/api/v1/machines', { items: [winMachine(true)] })
    .on('GET', '/api/v1/github/repositories', emptyRepos)
    .on('POST', '/api/v1/runner-plans', poolPlan('planned', 'planned'))
    .on('POST', '/api/v1/runner-plans/:id/apply', poolPlan('applying', 'running'))
    .on('GET', '/api/v1/runner-plans/:id', failedPlan);
  await mock.install(page);
  await page.goto('/setup/runners/add');
  await page.getByRole('radio', { name: /^Ephemeral/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Allow 1 ephemeral runner' }).click();
  await expect(
    page.getByText(/1 failed, 0 done\. build failed \(exit 1\)\./),
  ).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText(/Open Runners, remove it/)).toBeVisible();
  await expect(page.getByText('The runners are added.')).toHaveCount(0);
});

test('Add runners: the container backend not allowed is said on step 1', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/machines', { items: [winMachine(false)] })
    .on('GET', '/api/v1/github/repositories', emptyRepos);
  await mock.install(page);
  await page.goto('/setup/runners/add');
  await expect(
    page
      .getByRole('region', { name: 'Runners to add' })
      .getByText(/The container backend is not allowed on this machine/),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Allow it on the machine' }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Next' })).toBeDisabled();
  expect(mock.count('POST', '/api/v1/runner-plans')).toBe(0);
});

// OPS28: the kind of a runner is chosen on step 1; Ephemeral is preselected while a public repository uses the pool.
const kindPlan = (text: string, state: string, stepState: string) => ({
  id: 'rp10',
  area: 'runners',
  state,
  created_at: '2026-11-18T10:00:00Z',
  created_by: 'octo',
  steps: [{ id: 's1', action: 'add_runner', text, needs_yes: false, state: stepState }],
});
const repos = (visibility: string) => ({
  items: [repository({ visibility, pool_on: true })],
  stale: { stale: false },
});
const STEP = 'Add runner crsp-pool-1 on machine GameRoomPC';
const addApi = (visibility: string) =>
  signedIn()
    .on('GET', '/api/v1/machines', { items: [winMachine(true)] })
    .on('GET', '/api/v1/github/repositories', repos(visibility))
    .on('POST', '/api/v1/runner-plans', kindPlan(STEP, 'planned', 'planned'))
    .on('POST', '/api/v1/runner-plans/:id/apply', kindPlan(STEP, 'applying', 'running'))
    .on('GET', '/api/v1/runner-plans/:id', kindPlan(STEP, 'done', 'done'));
const planned = (mock: ReturnType<typeof signedIn>) =>
  mock.calls.find((c) => c.method === 'POST' && c.path === '/api/v1/runner-plans')?.body;

test('Add runners: a public repository preselects Ephemeral, with its reason, and adds the ceiling', async ({
  page,
}) => {
  const mock = addApi('public');
  await mock.install(page);
  await page.goto('/setup/runners/add');
  await expect(page.getByRole('radio', { name: /^Ephemeral/ })).toBeChecked();
  await expect(
    page.getByText('Ephemeral is chosen because a public repository uses the pool.'),
  ).toBeVisible();
  await expect(page.getByText('Most ephemeral runners at once')).toBeVisible();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(
    page.getByText(/Up to 1 ephemeral runner at once on GameRoomPC/),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Allow 1 ephemeral runner' }).click();
  await expect(page.getByText('The runners are added.')).toBeVisible({ timeout: 10_000 });
  expect(planned(mock)).toMatchObject({ os: 'linux', kind: 'ephemeral' });
});

test('Add runners: a pool of private repositories preselects Persistent and keeps dependencies', async ({
  page,
}) => {
  const mock = addApi('private');
  await mock.install(page);
  await page.goto('/setup/runners/add');
  // Persistent runs on the machine itself (runners R5.134): on this Windows machine that is a Windows runner.
  await page.getByRole('radio', { name: /^Windows/ }).click();
  await expect(page.getByRole('radio', { name: /^Persistent/ })).toBeChecked();
  await expect(
    page.getByText('Persistent is chosen because no public repository uses the pool.'),
  ).toBeVisible();
  await expect(
    page.getByRole('checkbox', { name: /Keep downloaded dependencies/ }),
  ).toBeChecked();
  await expect(
    page.getByText(/Temporary files of each job are always removed\./),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Add 1 runner' }).click();
  await expect(page.getByText('The runners are added.')).toBeVisible({ timeout: 10_000 });
  expect(planned(mock)).toMatchObject({
    os: 'windows',
    kind: 'persistent',
    keep_dependencies: true,
  });
});
