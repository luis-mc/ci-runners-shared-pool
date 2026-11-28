import { expect, test } from '@playwright/test';
import { expectDomLayout } from '../../../scripts/design/dom-layout';
import { busyQueue, signedIn } from './fixtures';
import { jobsAndDoctor } from './jobs-doctor-fixtures';

// CRSP391: Pool > Queue Analysis with 16 waiting jobs. One row per job (about a third of the old card);
// the running pane takes the full width of the page like the queue pane; the reason is said once above the table;
// at 393 px a row is two lines and the page does not scroll sideways.
for (const size of [
  { width: 1300, height: 900 },
  { width: 393, height: 852 },
  { width: 360, height: 780 },
]) {
  test(`queue rows at ${size.width} px`, async ({ page }) => {
    await page.setViewportSize(size);
    const mock = jobsAndDoctor(signedIn()).on('GET', '/api/v1/overview', busyQueue());
    await mock.install(page);
    await page.goto('/pool/queue');
    const table = page.getByRole('table', { name: 'Queue, in start order' });
    await expect(table).toBeVisible();
    await page.waitForLoadState('networkidle');
    const rows = table.locator('tbody tr[data-list-row]');
    await expect(rows).toHaveCount(16);

    // The reason for the 7 jobs is one message, not seven repeats.
    await expect(
      page.getByText(
        '7 jobs ask for the label pool-main, which no runner has. Check the repository',
        { exact: false },
      ),
    ).toHaveCount(1);
    await expect(page.getByText('waits to be placed')).toHaveCount(0);

    // The running pane is as wide as the queue pane (it was a 280 px column in the form field grid).
    const queuePane = page.getByRole('region', { name: 'Queue, in start order' });
    const runPane = page.getByRole('region', { name: 'Where running jobs are going' });
    const qw = (await queuePane.boundingBox())!.width;
    const rw = (await runPane.boundingBox())!.width;
    expect(Math.abs(qw - rw)).toBeLessThanOrEqual(2);
    // Its title is one line.
    const title = runPane.locator('.ds-pane__title').first();
    expect((await title.boundingBox())!.height).toBeLessThan(48);

    // One row is about 69 px at 1300 px (the Details button sets the height); a card per job was about 210 px.
    // The rows hold 16 jobs in 16 x 69 = 1104 px: the page chrome above the table is about 480 px, so all 16 jobs
    // do not fit one 900 px screen without scrolling (report CRSP391).
    const first = (await rows.nth(0).boundingBox())!;
    expect(first.height).toBeLessThanOrEqual(size.width === 1300 ? 72 : 180);
    await expectDomLayout(page, `queue rows ${size.width}`, { spaceVar: '--ds-space-' });
  });
}
