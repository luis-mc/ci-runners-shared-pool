// CRSP161: the machines screens are panes, the detail has one primary and one page foot, Remove is in its pane,
// and a machine that cannot load keeps its h1 (UX-4, UX-10, UX-17, UX-24, UX-37). At 360, 393 and 1300 px.
import { expect, test } from '@playwright/test';
import { failure, signedIn } from './fixtures';

for (const width of [360, 393, 1300]) {
  test.describe(`machines at ${width} px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test('the list is one pane titled Machines, with one primary', async ({ page }) => {
      await signedIn().install(page);
      await page.goto('/pool/machines');
      const pane = page.getByRole('region', { name: 'Machines' });
      await expect(pane.getByRole('table', { name: 'Machines' })).toBeVisible();
      await expect(pane.getByRole('textbox', { name: 'Search machines' })).toBeVisible();
      await expect(
        page.locator('button[data-primary], a[data-kind="primary"]'),
      ).toHaveCount(1);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      ).toBe(true);
    });

    test('the detail has one primary, one foot and Remove in its pane', async ({
      page,
    }) => {
      await signedIn().install(page);
      await page.goto('/pool/machines/m1');
      await expect(
        page.getByRole('heading', { level: 1, name: 'build-1' }),
      ).toBeVisible();
      await expect(page.getByRole('button', { name: 'Save' })).toHaveCount(1);
      await expect(page.getByRole('button', { name: 'Cancel' })).toHaveCount(1);
      await expect(
        page
          .getByRole('region', { name: 'Remove crsp' })
          .getByRole('button', { name: 'Remove crsp' }),
      ).toBeVisible();
      await expect(
        page.locator('button[data-primary]').filter({ hasText: 'Save' }),
      ).toHaveCount(1);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      ).toBe(true);
    });

    test('a machine that cannot load keeps its h1', async ({ page }) => {
      const mock = signedIn().on(
        'GET',
        '/api/v1/machines/:id',
        failure(500, 'internal', 'The controller could not answer.'),
      );
      await mock.install(page);
      await page.goto('/pool/machines/m1');
      await expect(page.locator('h1')).toHaveCount(1);
      await expect(page.getByText(/Could not load the machine/)).toBeVisible();
    });

    test('add a machine is inside a pane', async ({ page }) => {
      await signedIn().install(page);
      await page.goto('/pool/machines/add');
      await expect(page.getByRole('region', { name: 'New machine' })).toBeVisible();
    });
  });
}
