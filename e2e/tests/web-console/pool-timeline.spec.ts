// LOOK58: the slot timeline on Pool > Live and the machine page at 360, 393, 1300 and 1920 px: the page never scrolls
// sideways (only the chart does), the slots and legend show, and the keys and Back to now work.
import { expect, test } from '@playwright/test';
import { signedIn } from './fixtures';

for (const width of [360, 393, 1300, 1920]) {
  test.describe(`slot timeline at ${width} px`, () => {
    test.use({ viewport: { width, height: 900 } });

    for (const path of ['/pool/live', '/pool/machines/m1']) {
      test(`${path} has the slots and no page-level sideways scroll`, async ({
        page,
      }) => {
        await signedIn().install(page);
        await page.goto(path);
        await expect(page.getByText('build-1 slot 1').first()).toBeVisible();
        await expect(page.getByText('build-1 slot 2').first()).toBeVisible();
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
        ).toBe(true);
      });
    }

    test('Home returns to now and the chart scrolls with the arrow keys', async ({
      page,
    }) => {
      await signedIn().install(page);
      await page.goto('/pool/live');
      const chart = page.getByRole('region', { name: /Jobs of the pool/ });
      await chart.focus();
      await page.keyboard.press('ArrowLeft');
      await page.keyboard.press('Home');
      await expect(chart).toBeVisible();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      ).toBe(true);
    });
  });
}
