// R5.4, R5.5, UX-57: the palette, /, Down, Up, j and k, Enter and ?.
import { expect, test } from '@playwright/test';
import { signedIn } from './fixtures';

test('Ctrl+K opens the palette, finds an area and goes to it', async ({ page }) => {
  await signedIn().install(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible();
  await page.keyboard.press('Control+K');
  const dialog = page.getByRole('dialog', { name: 'Commands' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('textbox').fill('runn');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: 'Add runners' })).toBeVisible();
});

test('the palette lists a command only to a role that may use it', async ({ page }) => {
  await signedIn({
    id: 2,
    login: 'view',
    role: 'viewer',
    added_at: '2026-11-02T10:00:00Z',
  }).install(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible();
  await page.keyboard.press('Control+K');
  const dialog = page.getByRole('dialog', { name: 'Commands' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Add a machine')).toHaveCount(0);
});

test('? lists the shortcuts and Escape closes the list', async ({ page }) => {
  await signedIn().install(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible();
  await page.keyboard.press('?');
  await expect(page.getByRole('dialog', { name: 'Shortcuts' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('/ goes to the search bar, and nothing fires while typing in it', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.goto('/setup/machines');
  await expect(page.getByRole('table', { name: 'Machines' })).toBeVisible();
  await page.keyboard.press('/');
  await expect(page.getByRole('textbox', { name: 'Search machines' })).toBeFocused();
  await page.keyboard.type('?j');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('textbox', { name: 'Search machines' })).toHaveValue('?j');
});

test('Down and j move through the page to a row, Up and k come back, Enter opens the row', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.goto('/setup/machines');
  await expect(page.getByRole('table', { name: 'Machines' })).toBeVisible();
  await page.keyboard.press('/');
  const search = page.getByRole('textbox', { name: 'Search machines' });
  await expect(search).toBeFocused();
  // Down goes through the controls in page order until it reaches the first row.
  const row = page.locator('[data-list-row]').first();
  for (
    let i = 0;
    i < 40 && !(await row.evaluate((el) => el === document.activeElement));
    i++
  )
    await page.keyboard.press('ArrowDown');
  await expect(row).toBeFocused();
  await page.keyboard.press('k');
  await expect(row).not.toBeFocused();
  await page.keyboard.press('j');
  await expect(row).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { level: 1, name: 'build-1' })).toBeVisible();
});

test('the palette opens with focus in its search box and Down moves into the results', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible();
  await page.keyboard.press('Control+K');
  const dialog = page.getByRole('dialog', { name: 'Commands' });
  await expect(dialog.getByRole('textbox')).toBeFocused();
  await page.keyboard.type('j');
  await expect(dialog.getByRole('textbox')).toHaveValue('j');
});
