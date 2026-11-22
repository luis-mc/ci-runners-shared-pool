// CRSP210: a table never cuts a value and never lets a cell cover the next one or the action column. The DOM layout
// judge's `clipped-text` rule runs on the Jobs Live and Done tables and the Runners table at 360, 393 and
// 1300 px, with seeded data that has long workflow paths, long branch names, long wait reasons and long state reasons.
import { expect, test } from '@playwright/test';
import { measureDomLayout, judgeLayout } from '../../../scripts/design/dom-layout';
import { failure, runner, signedIn } from './fixtures';
import { jobsAndDoctor, queuedJob, runningJob, doneJob } from './jobs-doctor-fixtures';

const WORKFLOW = '.github/workflows/continuous-integration-and-release-pipeline.yml';
const BRANCH = 'dependabot/npm_and_yarn/packages/ui/some-very-long-dependency-name-8.3.8';
const WAIT =
  'waits for a free slot on any Linux machine that carries every one of its labels, the repository parallel limit is reached';
const STATE =
  'Container engine could not be started: cannot connect to the Docker daemon at unix:///var/run/docker.sock and persistent runners are not allowed on this machine';
const RUN_ID = 44723830522;

const longRun = (over: Record<string, unknown>) => ({
  id: RUN_ID,
  attempt: 1,
  attempts: [1],
  repository: 'octo/app-with-a-long-repository-name',
  repository_id: 7,
  workflow: WORKFLOW,
  branch: BRANCH,
  head_sha: 'a1b2c3d4e5f6',
  task_id: 'OPS34',
  trigger: 'push',
  html_url: `https://github.com/octo/app/actions/runs/${RUN_ID}`,
  state: 'done',
  result: 'success',
  created_at: '2026-11-13T09:00:00Z',
  completed_at: '2026-11-13T09:05:00Z',
  jobs_total: 3,
  jobs_done: 3,
  jobs_failed: 0,
  total_seconds: 300,
  ...over,
});

function seeded() {
  const mock = jobsAndDoctor(signedIn());
  const page = (items: unknown[]) => ({
    items,
    total: items.length,
    stale: { stale: false },
  });
  const job = { workflow: WORKFLOW, branch: BRANCH, run_id: RUN_ID };
  mock.on('GET', '/api/v1/jobs', (call: { query: URLSearchParams }) => {
    const state = call.query.get('state');
    if (state === 'queued')
      return page([{ ...queuedJob, ...job, wait_reason: undefined, wait_text: WAIT }]);
    if (state === 'running') return page([{ ...runningJob, ...job }]);
    return page([doneJob(1, job)]);
  });
  mock.on('GET', '/api/v1/runs', (call: { query: URLSearchParams }) => {
    const state = call.query.get('state');
    if (state === 'queued') return page([]);
    if (state === 'running')
      return page([
        longRun({
          state: 'running',
          result: undefined,
          completed_at: undefined,
          total_seconds: undefined,
          jobs_done: 1,
          eta: { p50: 300, lo: 240, hi: 420 },
        }),
      ]);
    return page([longRun({})]);
  });
  mock.on('GET', '/api/v1/runners', {
    items: [
      runner(),
      runner({
        id: 'r2',
        name: 'crsp-persistent-pool-with-a-very-long-runner-name-3',
        state: 'failed',
        failed_reason: STATE,
        reason: STATE,
      }),
      runner({
        id: 'r3',
        name: 'crsp-container-4',
        isolation: 'container',
        keeps_state: false,
        state: 'offline',
        reason: STATE,
      }),
    ],
  });
  mock.on('GET', '/api/v1/runners/:id', failure(404, 'not_found', 'No such runner.'));
  return mock;
}

const SCREENS = [
  { name: 'Jobs, Live', path: '/pool/jobs', ready: 'All runs' },
  { name: 'Jobs, Queue', path: '/pool/jobs?state=queued', ready: 'Queue runs' },
  { name: 'Jobs, Running', path: '/pool/jobs?state=running', ready: 'Running runs' },
  { name: 'Jobs, Done', path: '/pool/jobs?state=done', ready: 'Done runs' },
  {
    name: 'Jobs, Queue jobs',
    path: '/pool/jobs?state=queued&view=jobs',
    ready: 'Queue jobs',
  },
  { name: 'Runners', path: '/setup/runners', ready: 'Runners on' },
];

for (const s of SCREENS) {
  test(`no clipped text in ${s.name} with long values`, async ({ page }) => {
    await seeded().install(page);
    await page.goto(s.path);
    await expect(page.locator('table.ds-table').first()).toBeAttached();
    await expect(page.getByRole('heading', { level: 1 }).first()).toBeVisible();
    const found = await measureDomLayout(page, {
      ready: () =>
        page.evaluate(
          () =>
            new Promise<void>((done) =>
              requestAnimationFrame(() => requestAnimationFrame(() => done())),
            ),
        ),
    });
    const { unknown, stale } = judgeLayout(
      s.name,
      found.filter((p) => p.rule === 'clipped-text'),
    );
    expect([...unknown, ...stale]).toEqual([]);
  });
}
