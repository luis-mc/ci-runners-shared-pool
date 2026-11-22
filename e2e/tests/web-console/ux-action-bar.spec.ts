// OPS47: the action bar (component 48, UX-42) is always at the foot of the window and never covers the content.
// On every console page that draws an EditBar or an ActionBar, with the page scrolled to the top the bar is inside
// the viewport, and with the page scrolled to its end the last row of the content ends above the bar.
import { expect, test } from '@playwright/test';
import { openRoute } from './ux-guards';
import { consoleRoutes } from './routes';

/** Runs in the page: the leaf elements of the main content that are partly under the bar. */
const UNDER_BAR = `(() => {
  const b = document.querySelector('.ds-action-bar[data-sticky="true"]');
  if (!b) return [];
  const barTop = b.getBoundingClientRect().top;
  const lines = [];
  for (const el of document.querySelectorAll('.ds-main *')) {
    if (b.contains(el) || el.contains(b) || el.children.length > 0) continue;
    // A box with its own clip or scroll (a table wrapper) or a sticky head keeps its own place: not page content.
    // Content of a closed disclosure has a box but is not drawn.
    if (!el.checkVisibility({ contentVisibilityAuto: true, visibilityProperty: true })) continue;
    let own = false;
    for (let p = el; p && p !== document.body; p = p.parentElement) {
      const st = getComputedStyle(p);
      if (st.overflow !== 'visible' || st.position === 'sticky') own = true;
    }
    if (own) continue;
    const r = el.getBoundingClientRect();
    if (r.height === 0 || r.width === 0) continue;
    if (r.top < barTop && r.bottom > barTop + 1) lines.push(el.tagName + ' ' + (el.textContent || '').slice(0, 30));
  }
  return lines;
})()`;

const WIDTHS = [360, 393, 1300];
// The machine page has no sample id in the route table; its id in the mocked data is m1.
const URLS = [
  ...new Set([
    '/setup/machines/m1',
    ...consoleRoutes()
      .filter((r) => !r.signedOut)
      .map((r) => r.url),
  ]),
];

for (const width of WIDTHS) {
  test.describe(`action bar at ${width} px`, () => {
    for (const url of URLS) {
      test(`${url}: the bar stays at the foot and covers nothing`, async ({ page }) => {
        await openRoute(page, { url, signedOut: false }, { width });
        const bar = page.locator('.ds-action-bar[data-sticky="true"]');
        if ((await bar.count()) === 0) return; // a page without a bar has nothing to guard
        await expect(bar).toHaveCount(1); // one bar per screen (UX-71)
        await page.evaluate(() => window.scrollTo(0, 0));
        const top = await bar.boundingBox();
        expect(top, 'the bar is drawn').not.toBeNull();
        const vh = await page.evaluate(() => innerHeight);
        expect(top!.y).toBeGreaterThanOrEqual(0);
        expect(top!.y + top!.height).toBeLessThanOrEqual(vh + 1);
        // Scrolled to the end: the bar is still in view and the last row of the content is not under it.
        await page.evaluate(() =>
          window.scrollTo(0, document.documentElement.scrollHeight),
        );
        const end = await bar.boundingBox();
        expect(end!.y + end!.height).toBeLessThanOrEqual(vh + 1);
        const under = await page.evaluate<string[]>(UNDER_BAR);
        expect(under).toEqual([]);
      });
    }
  });
}
