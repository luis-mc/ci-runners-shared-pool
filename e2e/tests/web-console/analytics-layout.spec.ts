// LOOK65: the four Analytics tabs, the slot timeline's reserved and timer ran out bars and the Queue chips at 360, 393 and
// 1300 px (DOM layout): the page never scrolls sideways, each screen shows the panes and columns of the approved prototype.
import { expect, test } from '@playwright/test';
import { overview, signedIn } from './fixtures';
import { at, insightsApi } from './insights-fixtures';

const analytics = () =>
  insightsApi(signedIn())
    .on('GET', '/api/v1/jobs/stats', { days: [], hours: [], forecast_points: [] })
    .on('GET', '/api/v1/jobs/groups', { items: [] })
    .on('GET', '/api/v1/jobs', { items: [], total: 0, stale: { stale: false } });

for (const width of [360, 393, 1300]) {
  test.describe(`analytics at ${width} px`, () => {
    test.use({ viewport: { width, height: 900 } });
    const noSideways = async (page: import('@playwright/test').Page) =>
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      ).toBe(true);

    test('Suggestions: the five columns, the fix inline and the one primary', async ({
      page,
    }) => {
      await analytics().install(page);
      await page.goto('/analytics');
      const pane = page.getByRole('region', { name: 'Suggestions, best first' });
      for (const h of ['#', 'Change', 'Sure?', 'Saves a week', 'Who decides'])
        await expect(
          pane.getByRole('columnheader', { name: h, exact: true }),
        ).toBeVisible();
      await expect(pane.getByText(/^Fix: /).first()).toBeVisible();
      await expect(
        page.getByRole('button', { name: 'Review the first change' }),
      ).toBeVisible();
      await noSideways(page);
    });

    test('Charts: the machine CPU and the reservation example come first', async ({
      page,
    }) => {
      await analytics().install(page);
      await page.goto('/analytics/charts');
      await expect(
        page.getByRole('region', { name: 'build-1: CPU today' }),
      ).toBeVisible();
      await expect(
        page.getByRole('region', { name: 'Free CPU, with and without a reservation' }),
      ).toBeVisible();
      await noSideways(page);
    });

    test('Run elsewhere: a plain table', async ({ page }) => {
      await analytics().install(page);
      await page.goto('/analytics/elsewhere');
      await expect(page.getByRole('region', { name: 'Run elsewhere' })).toBeVisible();
      await expect(page.getByRole('searchbox')).toHaveCount(0);
      await noSideways(page);
    });

    test('Insights: Waits and the Runner timeline below the tiles', async ({ page }) => {
      await analytics().install(page);
      await page.goto('/analytics/insights');
      const waits = page.getByRole('region', { name: 'Waits' });
      await expect(waits.getByRole('columnheader', { name: 'Slowest 10%' })).toBeVisible();
      await expect(page.getByRole('region', { name: 'Runner timeline' }).first()).toBeVisible();
      await noSideways(page);
    });

    test('Queue Analysis has the Reserved chip and the slot timeline draws a reserved bar', async ({
      page,
    }) => {
      const queued = {
        id: 5,
        repository: 'octo/app',
        run_id: 9,
        run_attempt: 1,
        github_job_id: 5,
        job_name: 'big',
        state: 'queued',
        labels: [],
        queue_position: 1,
        wait_reason: 'reserved',
      };
      await analytics()
        .on('GET', '/api/v1/overview', {
          ...overview(),
          queued: [queued],
          queued_count: 1,
        })
        .on('GET', '/api/v1/timeline', {
          now: at(0),
          from: at(-360),
          to: at(60),
          more_before: false,
          rows: [
            {
              key: 'm1:1',
              machine_id: 'm1',
              machine_name: 'build-1',
              slot: 1,
              label: 'build-1 slot 1',
            },
            {
              key: 'm1:waiting',
              machine_id: 'm1',
              machine_name: 'build-1',
              label: 'build-1 waiting',
            },
          ],
          bars: [
            {
              id: 'job-5',
              job_id: 5,
              row_id: 'm1:waiting',
              state: 'reserved',
              label: 'big',
              start: at(0),
              detail: 'is reserved: build-1 keeps room free for it',
            },
            {
              id: 'job-6',
              job_id: 6,
              row_id: 'm1:1',
              state: 'timer_ran_out',
              label: 'slow',
              start: at(-40),
              expires_at: at(1),
              detail: 'slow ran out of time',
            },
          ],
        })
        .install(page);
      await page.goto('/pool/queue');
      await expect(
        page.getByRole('button', { name: 'Reserved', exact: true }),
      ).toBeVisible();
      await expect(
        page.locator('.ds-timeline__item[data-state="reserved"]').first(),
      ).toBeVisible();
      await expect(
        page.locator('.ds-timeline__item[data-state="timer_ran_out"]').first(),
      ).toBeVisible();
      await noSideways(page);
    });
  });
}
