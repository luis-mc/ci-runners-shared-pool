// T10 (layout part, R6.3, R6.9): each page fits 360, 393 and 1300 px with no sideways scroll, one h1, and the frame's
// gutter holds (nothing touches the window edge).
import { expect, test } from '@playwright/test';
import { PAGES, WIDTHS } from './pages.ts';

for (const width of WIDTHS) {
  test.describe(`layout at ${width} px`, () => {
    test.use({ viewport: { width, height: 900 } });
    for (const path of PAGES) {
      test(`${path} has no sideways scroll`, async ({ page }) => {
        await page.goto(path);
        const wide = await page.evaluate(
          () => document.documentElement.scrollWidth - window.innerWidth,
        );
        expect(wide, 'the page is wider than the window').toBeLessThanOrEqual(0);
        await expect(page.locator('h1')).toHaveCount(1);
        const gutter = await page.evaluate(() => {
          const main = document.querySelector('main')!.getBoundingClientRect();
          return { left: main.left, right: window.innerWidth - main.right };
        });
        expect(gutter.left).toBeGreaterThanOrEqual(0);
        expect(gutter.right).toBeGreaterThanOrEqual(0);
        const text = await page.evaluate(() => {
          const h1 = document.querySelector('h1')!.getBoundingClientRect();
          return h1.left;
        });
        // The page gutter is at least 16 px from the window edge (--ds-page-gutter).
        expect(text).toBeGreaterThanOrEqual(16);
      });
    }
  });
}
