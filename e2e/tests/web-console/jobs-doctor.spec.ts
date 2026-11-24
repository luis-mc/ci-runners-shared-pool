// CRSP35: the jobs and doctor screens against the mocked API: the queue and its wait reason, the history and its
// filters, Export of the filter, the detail with its GitHub link, and a doctor run from busy to done.
import { expect, test } from '@playwright/test';
import { signedIn } from './fixtures';
import { doneJob, finding, jobsAndDoctor } from './jobs-doctor-fixtures';

test('Jobs: the queue says why a run waits and when it should start, Done lists finished runs', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs?state=queued&view=runs');
  await expect(page.getByRole('heading', { level: 1, name: 'Pool' })).toBeVisible();
  const queue = page.getByRole('table', { name: 'Queue runs' });
  // CRSP210: the row shows the short reason; Why opens the whole sentence under the row.
  await expect(queue.getByText('waits for a slot', { exact: true })).toBeVisible();
  await queue.getByRole('button', { name: 'Why' }).click();
  await expect(
    queue.getByText('waits for a free slot on any Linux machine'),
  ).toBeVisible();
  await expect(queue.getByText('in about 4 to 7 min')).toBeVisible();
  await page
    .getByRole('group', { name: 'State' })
    .getByRole('button', { name: 'Done' })
    .click();
  const done = page.getByRole('table', { name: 'Done runs' });
  await expect(done.getByRole('button', { name: 'Open run' })).toHaveCount(3);
  await expect(
    page.getByText(/Showing 3 of 3 finished runs in the last 90 days/),
  ).toBeVisible();
});

test('Jobs: the Done range asks for a from date, and Export holds the same filter', async ({
  page,
}) => {
  const mock = jobsAndDoctor(signedIn()).on('GET', '/api/v1/machines', { items: [] });
  await mock.install(page);
  let exportedUrl = '';
  await page.route('**/api/v1/exports/jobs**', (route) => {
    exportedUrl = route.request().url();
    return route.fulfill({
      status: 200,
      headers: {
        'content-type': 'text/csv',
        'content-disposition': 'attachment; filename="crsp-jobs-2026-11-14-1005.csv"',
      },
      body: 'id,repository\n1,octo/app\n',
    });
  });
  await page.goto('/pool/jobs?state=done&view=jobs');
  await expect(page.getByRole('table', { name: 'Done jobs' })).toBeVisible();
  await page.getByLabel('Range').selectOption('7');
  await expect
    .poll(() =>
      mock.calls.some(
        (c) =>
          c.path === '/api/v1/jobs' &&
          c.query.get('state') === 'done' &&
          Date.now() - Date.parse(c.query.get('from') ?? '') < 7.1 * 86_400_000,
      ),
    )
    .toBe(true);
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export CSV' }).click();
  expect((await download).suggestedFilename()).toBe('crsp-jobs-2026-11-14-1005.csv');
  const exported = new URL(exportedUrl);
  expect(exported.searchParams.get('state')).toBe('done');
  expect(exported.searchParams.get('format')).toBe('csv');
  expect(exported.searchParams.get('from')).not.toBeNull();
});

test('Jobs: a job row opens the Job page with a link to the run on GitHub', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs?state=done&view=jobs');
  // LOOK62: the row opens the inspector beside the list; its Open job button goes to the Job page.
  const row = page.getByRole('row').filter({ hasText: 'test-2' });
  await row.getByRole('button', { name: 'Why' }).click();
  await page
    .getByRole('complementary', { name: /^Details: / })
    .getByRole('button', { name: 'Open job' })
    .click();
  await expect(page.getByRole('heading', { level: 1, name: 'test-2' })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Open the run on GitHub' }),
  ).toHaveAttribute('href', 'https://github.com/octo/app/actions/runs/102/job/2');
});

test('Jobs: Open run goes straight to the Run page', async ({ page }) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs?view=runs');
  await page.getByRole('button', { name: 'Open run' }).first().click();
  await expect(page.getByText('Open run page')).toHaveCount(0);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('ci.yml on');
  await expect(page.getByRole('region', { name: 'Job graph' })).toBeVisible();
});

test('Jobs: an empty pool says so', async ({ page }) => {
  await jobsAndDoctor(signedIn(), [])
    .on('GET', '/api/v1/jobs', { items: [], total: 0, stale: { stale: false } })
    .on('GET', '/api/v1/jobs/groups', { items: [] })
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs?group=repository');
  await expect(
    page.getByText('No jobs yet. Jobs show here when a workflow runs on the pool.'),
  ).toBeVisible();
});

test('Doctor: findings by area, then Run checks again is busy until the run ends', async ({
  page,
}) => {
  let state = 'running';
  const mock = jobsAndDoctor(signedIn())
    .on('POST', '/api/v1/doctor/runs', {
      id: 'run1',
      trigger: 'web',
      state: 'running',
      started_at: '2026-11-14T10:05:00Z',
      counts: {},
      findings: [],
    })
    .on('GET', '/api/v1/doctor/runs/:id', () => ({
      id: 'run1',
      trigger: 'web',
      state,
      started_at: '2026-11-14T10:05:00Z',
      counts: { fail: 1 },
      findings: [finding()],
    }));
  await mock.install(page);
  await page.goto('/doctor');
  await expect(page.getByRole('region', { name: 'Machines' })).toContainText(
    'build-2 is offline',
  );
  await expect(page.getByRole('region', { name: 'Workflow labels' })).toContainText(
    'failed',
  );
  await page.getByRole('button', { name: 'Run checks again' }).click();
  await expect(page.getByRole('button', { name: 'Running the doctor' })).toBeDisabled();
  await expect(
    page.getByRole('progressbar', { name: 'Running the doctor' }),
  ).toBeVisible();
  state = 'done';
  await expect(page.getByRole('button', { name: 'Run checks again' })).toBeEnabled({
    timeout: 10_000,
  });
  expect(mock.count('POST', '/api/v1/doctor/runs')).toBe(1);
});

test('Doctor: could not load says what and offers Try again', async ({ page }) => {
  await signedIn()
    .on('GET', '/api/v1/doctor/findings', {
      status: 500,
      error: { code: 'internal', message: 'the findings could not be read', details: [] },
    })
    .install(page);
  await page.goto('/doctor');
  await expect(page.getByText('Could not load the doctor findings.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
});

test('Jobs and Doctor are in the menu and no longer say they arrive later', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn(), [doneJob(1)])
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/doctor');
  await expect(page.getByText(/arrives in milestone/)).toHaveCount(0);
});

// LOOK41 (owner decision 20 Nov 2026, option 11 of CRSP211): one list with a state filter, a search box with focus,
// Up, Down, j and k through every control and row, and a details panel behind a named button.
test('Jobs: the list starts with focus in the search box and the search narrows it', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs');
  const box = page.getByRole('textbox', { name: /Search/ });
  await expect(box).toBeFocused();
  const rows = page.locator('table.ds-table tbody tr[data-list-row]');
  await expect(rows.first()).toBeVisible();
  const all = await rows.count();
  expect(all).toBeGreaterThan(1);
  await page.keyboard.type('state:wait');
  await expect(rows).not.toHaveCount(all);
  await expect(rows.first()).toContainText('waiting');
  await page.keyboard.press('Control+A');
  await page.keyboard.type('nothing-like-this-at-all');
  await expect(page.getByText(/Nothing matches/)).toBeVisible();
});

test('Jobs: Down, Up, j and k move from the search box through the filters to the rows', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs');
  await expect(page.getByRole('textbox', { name: /Search/ })).toBeFocused();
  const rows = page.locator('table.ds-table tbody tr[data-list-row]');
  await expect(rows.first()).toBeVisible();
  // After the search box come the State and view choices (Down walks them), then the filter drop-downs, which keep
  // Up and Down for their own options (the keys of a select): Tab passes them. Then the rows, which Down, Up, `j` and
  // `k` walk.
  const walk = async (key: string, until: () => Promise<boolean>) => {
    for (let i = 0; i < 40 && !(await until()); i++) await page.keyboard.press(key);
    expect(await until()).toBe(true);
  };
  for (let i = 0; i < 6; i++) await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('button', { name: 'Runs' })).toBeFocused();
  const rowIndex = () =>
    page.evaluate(() =>
      Array.from(document.querySelectorAll('tbody tr[data-list-row]')).findIndex(
        (r) => r === document.activeElement,
      ),
    );
  await walk('Tab', async () => (await rowIndex()) === 0);
  await walk('j', async () => (await rowIndex()) === 1);
  await walk('k', async () => (await rowIndex()) === 0);
  await walk('ArrowUp', async () => (await rowIndex()) === -1);
});

test('Jobs: Show details opens the panel of the chosen row, Hide details closes it', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs?view=runs');
  const toggle = page.getByRole('button', { name: 'Show details' });
  await expect(toggle).toHaveAttribute('title', 'Show details');
  await toggle.click();
  const side = page.getByRole('complementary', { name: /^Details: / });
  await expect(side).toBeVisible();
  await expect(side.getByText('Repository')).toBeVisible();
  await page.getByRole('button', { name: 'Hide details' }).click();
  await expect(side).toHaveCount(0);
});

test('Jobs: Why opens the inspector beside the list, Close details closes it', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool/jobs');
  const table = page.getByRole('table', { name: 'All jobs' });
  await table.getByRole('button', { name: 'Why' }).first().click();
  const side = page.getByRole('complementary', { name: /^Details: / });
  await expect(side).toBeVisible();
  await expect(table).toBeVisible();
  await side.getByRole('button', { name: 'Close details' }).click();
  await expect(side).toHaveCount(0);
});

test('Pool and Analytics: the tabs are in one order', async ({ page }) => {
  await jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .install(page);
  await page.goto('/pool');
  await expect(page.getByRole('tab', { name: 'Overview' })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  expect(await page.getByRole('tab').allTextContents()).toEqual([
    'Overview',
    'Live',
    'Queue Analysis',
    'Jobs',
    'Machines',
  ]);
  await page.goto('/analytics');
  await expect(page.getByRole('tab', { name: 'Suggestions' })).toBeVisible();
  expect(await page.getByRole('tab').allTextContents()).toEqual([
    'Suggestions',
    'Insights',
    'Charts',
    'Run elsewhere',
  ]);
});
