// CRSP162 (CORE-1..7, UX-4, UX-17): the repository screens at 360, 393 and 1300 px. Every titled part is a pane,
// one primary button per screen, one h1 also when the page failed to load, no sideways page scroll, and no
// serious or critical axe violation. Layout rules of the shared parts are in design-layout.spec.ts.
import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { failure, signedIn } from './fixtures';
import { jobsAndDoctor } from './jobs-doctor-fixtures';

const WIDTHS = [360, 393, 1300];

const SCREENS = [
  {
    name: 'list',
    path: '/setup/pool',
    panes: ['Using the pool', 'Not using the pool'],
    primaries: ['Turn on'],
  },
  {
    name: 'detail',
    path: '/setup/pool/octo/app',
    panes: [
      'State',
      'Policy',
      'Hosted fallback',
      'Extra packages',
      'Job sizes',
      'Cost table',
    ],
    primaries: ['Save'],
  },
  {
    name: 'turn on the pool',
    path: '/setup/pool/turn-on',
    panes: ['Repositories to turn on'],
    primaries: ['Next'],
  },
];

for (const width of WIDTHS) {
  for (const screen of SCREENS) {
    test(`${screen.name}: panes, one primary, no overflow at ${width} px`, async ({
      page,
    }) => {
      await jobsAndDoctor(signedIn()).install(page);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(screen.path);
      await expect(page.locator('h1')).toHaveCount(1);
      await page.waitForLoadState('networkidle');
      for (const title of screen.panes)
        await expect(
          page
            .locator('.ds-pane')
            .getByRole('heading', { level: 2, name: new RegExp(`^${title}`) }),
        ).toBeVisible();
      const primaries = await page
        .locator('[data-kind="primary"]')
        .evaluateAll((els) => els.map((e) => (e.textContent ?? '').trim()));
      expect(primaries).toEqual(screen.primaries);
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      );
      expect(overflow).toBeLessThanOrEqual(0);
      const result = await new AxeBuilder({ page }).analyze();
      expect(
        result.violations.filter(
          (v) => v.impact === 'serious' || v.impact === 'critical',
        ),
      ).toEqual([]);
    });
  }

  test(`detail keeps its h1 when it cannot load at ${width} px`, async ({ page }) => {
    await jobsAndDoctor(
      signedIn().on(
        'GET',
        '/api/v1/repositories/:id',
        failure(500, 'internal', 'The controller could not answer.'),
      ),
    ).install(page);
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/setup/pool/octo/app');
    await expect(page.getByRole('heading', { level: 1, name: 'octo/app' })).toBeVisible();
    await expect(page.getByText('Could not load the repository.')).toBeVisible();
  });
}
