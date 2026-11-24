// LOOK62: Pool > Jobs as approved, at 360, 393 and 1300 px: the Jobs list first with a Why button on every row, the
// Filters pane open with its groups, the inspector beside the list, and a reserved job. The DOM layout of each state
// is judged by the design kit and the page never scrolls sideways.
import { expect, test } from '@playwright/test';
import { expectDomLayout } from '../../../scripts/design/dom-layout';
import { signedIn, type Call } from './fixtures';
import { doneJob, jobsAndDoctor, queuedJob, runningJob } from './jobs-doctor-fixtures';

const reservedJob = {
  ...queuedJob,
  id: 9,
  run_id: 103,
  job_name: 'build-reserved',
  node_id: 'build-reserved',
  wait_reason: 'reserved',
  wait_text: undefined,
};

function api() {
  // The later handler wins: the jobs of every state, the reserved one among the waiting.
  return jobsAndDoctor(signedIn())
    .on('GET', '/api/v1/machines', { items: [] })
    .on('GET', '/api/v1/jobs', (call: Call) => {
      const state = call.query.get('state');
      const items =
        state === 'queued'
          ? [queuedJob, reservedJob]
          : state === 'running'
            ? [runningJob]
            : [doneJob(1)];
      return { items, total: items.length, stale: { stale: false } };
    });
}

const noSideScroll = (page: import('@playwright/test').Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

for (const width of [360, 393, 1300]) {
  test.describe(`Pool > Jobs at ${width} px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test('opens on the Jobs list with Why on every row and the filters pane open', async ({
      page,
    }) => {
      await api().install(page);
      await page.goto('/pool/jobs');
      const table = page.getByRole('table', { name: 'All jobs' });
      await expect(table).toBeVisible();
      await expect(table.getByRole('button', { name: 'Why' })).toHaveCount(4);
      const pane = page.getByRole('region', { name: 'Filters' });
      for (const name of ['Repository', 'Machine', 'Runner type', 'Branch'])
        await expect(pane.getByLabel(name)).toBeVisible();
      expect(await noSideScroll(page)).toBe(true);
      await page.waitForLoadState('networkidle');
      await expectDomLayout(page, 'pool jobs list', { spaceVar: '--ds-space-' });
    });

    test('Why opens the inspector beside the list and the list stays', async ({
      page,
    }) => {
      await api().install(page);
      await page.goto('/pool/jobs');
      const table = page.getByRole('table', { name: 'All jobs' });
      await table.getByRole('button', { name: 'Why' }).first().click();
      const side = page.getByRole('complementary', { name: /^Details: / });
      await expect(side).toBeVisible();
      await expect(table).toBeVisible();
      expect(await noSideScroll(page)).toBe(true);
      await page.waitForLoadState('networkidle');
      await expectDomLayout(page, 'pool jobs inspector', { spaceVar: '--ds-space-' });
    });

    test('a reserved job shows as Reserved', async ({ page }) => {
      await api().install(page);
      await page.goto('/pool/jobs');
      const row = page.getByRole('row').filter({ hasText: 'build-reserved' });
      await expect(row.getByText('Reserved', { exact: true })).toBeVisible();
      await row.getByRole('button', { name: 'Why' }).click();
      await expect(
        page
          .getByRole('complementary', { name: /^Details: / })
          .getByText(/room another job holds/)
          .first(),
      ).toBeVisible();
      expect(await noSideScroll(page)).toBe(true);
    });
  });
}

test('Runs stays as the second view', async ({ page }) => {
  await api().install(page);
  await page.goto('/pool/jobs');
  await page
    .getByRole('group', { name: 'Show runs or jobs' })
    .getByRole('button', { name: 'Runs' })
    .click();
  await expect(page.getByRole('table', { name: 'All runs' })).toBeVisible();
});
