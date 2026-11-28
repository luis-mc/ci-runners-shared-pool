// T8 (R5.19): the static search finds a known doc, filters the list, and says what it found.
import { expect, test } from '@playwright/test';

test('search finds the install guide and says how many pages match', async ({ page }) => {
  await page.goto('/docs');
  const box = page.getByRole('searchbox', { name: 'Search the docs' });
  await expect(box).toBeVisible();
  await box.fill('attestation');
  await expect(page.getByRole('status')).toContainText(/\d+ pages? match/);
  await expect(
    page.getByRole('link', { name: 'Install and check a download' }),
  ).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Speed comparison workflow' }),
  ).toBeHidden();
});

test('search says when nothing matches, and clearing it shows every page', async ({
  page,
}) => {
  await page.goto('/docs');
  const box = page.getByRole('searchbox', { name: 'Search the docs' });
  await box.fill('zzzzqqqq');
  await expect(page.getByRole('status')).toContainText('No page matches');
  await box.fill('');
  await expect(
    page.getByRole('link', { name: 'Speed comparison workflow' }),
  ).toBeVisible();
});

test('the Download page puts the visitor system first', async ({ browser }) => {
  const context = await browser.newContext({
    userAgent:
      'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/130 Safari/537.36',
  });
  const page = await context.newPage();
  await page.goto('/download');
  const first = page.locator('main section.ds-pane').first();
  await expect(first).toHaveAttribute('id', 'os-linux');
  await context.close();
});
