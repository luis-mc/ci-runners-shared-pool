// LOOK58c: Pool > Machines cards with health and job stats, and the machine page (/pool/machines/:machineId), at 360,
// 393 and 1300 px: no sideways scroll, the card shows the numbers, the switch changes the window, the title opens the
// page, and a value the machine could not give reads "unknown on <system>".
import { expect, test } from '@playwright/test';
import { machine, machineHealth, signedIn } from './fixtures';

const unknownWindows = machineHealth({
  boot: { unknown_reason: 'the boot time is not readable' },
  swap: { unknown_reason: 'no page file counter' },
  thermal: { unknown_reason: 'no sensor' },
  load: { load_unknown_reason: 'Windows has no load average' },
});

for (const width of [360, 393, 1300]) {
  test.describe(`machine health at ${width} px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test('the card shows health badges, key numbers, job stats and runners', async ({
      page,
    }) => {
      await signedIn().install(page);
      await page.goto('/pool/machines');
      const card = page.getByRole('region', { name: 'build-1' });
      await expect(card.getByText('3 days 3 h')).toBeVisible();
      await expect(card.getByText('Days until full')).toBeVisible();
      await expect(card.getByText('Average run time')).toBeVisible();
      await expect(card.getByText('crsp-linux-1')).toBeVisible();
      await expect(card.getByRole('link', { name: 'build', exact: true })).toBeVisible();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      ).toBe(true);
    });

    test('the 24 h / 7 days switch asks for the other window', async ({ page }) => {
      const mock = signedIn();
      await mock.install(page);
      await page.goto('/pool/machines');
      const card = page.getByRole('region', { name: 'build-1' });
      await card.getByRole('button', { name: '7 days' }).click();
      await expect(card.getByRole('button', { name: '7 days' })).toHaveAttribute(
        'aria-pressed',
        'true',
      );
      await expect
        .poll(() =>
          mock.calls.some(
            (c) => c.path.endsWith('/job-stats') && c.query.get('window') === '7d',
          ),
        )
        .toBe(true);
    });

    test('a value the machine could not give reads unknown on its system', async ({
      page,
    }) => {
      const mock = signedIn()
        .on('GET', '/api/v1/machines', { items: [machine({ os: 'windows' })] })
        .on('GET', '/api/v1/machines/:id', machine({ os: 'windows' }))
        .on('GET', '/api/v1/machines/:id/health', unknownWindows);
      await mock.install(page);
      await page.goto('/pool/machines');
      const card = page.getByRole('region', { name: 'build-1' });
      await expect(
        card.getByText('unknown on Windows: Windows has no load average'),
      ).toBeVisible();
      await expect(card.getByText(/unknown on Linux/)).toHaveCount(0);
    });

    test('the title opens the machine page with every section', async ({ page }) => {
      await signedIn().install(page);
      await page.goto('/pool/machines');
      await page.getByRole('link', { name: 'build-1' }).click();
      await expect(page).toHaveURL(/\/pool\/machines\/m1$/);
      await expect(
        page.getByRole('heading', { level: 1, name: 'build-1' }),
      ).toBeVisible();
      for (const name of [
        'Key numbers',
        'Disks',
        'Memory and swap',
        'Network and clock',
        'Restarts, sleep and power',
        'Versions and Docker',
        'Heat and load',
        'Job stats',
        'Jobs over time',
      ])
        await expect(page.getByRole('region', { name })).toBeVisible();
      await expect(page.locator('h1')).toHaveCount(1);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      ).toBe(true);
    });

    test('the machine page says unknown on Windows, never blank', async ({ page }) => {
      const mock = signedIn()
        .on('GET', '/api/v1/machines/:id', machine({ os: 'windows' }))
        .on('GET', '/api/v1/machines/:id/health', unknownWindows);
      await mock.install(page);
      await page.goto('/pool/machines/m1');
      await expect(page.getByRole('region', { name: 'Heat and load' })).toContainText(
        'unknown on Windows: no sensor',
      );
      await expect(page.getByText(/unknown on Linux/)).toHaveCount(0);
    });
  });
}
