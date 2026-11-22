// R5.2, R5.8, R5.9: a deep link opens its screen after sign-in; an unknown path shows not found.
import { expect, test } from '@playwright/test';
import { failure, signedIn } from './fixtures';

test('a signed-out visitor is sent to sign-in and the deep link is kept', async ({
  page,
}) => {
  const mock = signedIn().on(
    'GET',
    '/api/v1/me',
    failure(401, 'unauthenticated', 'Sign in.'),
  );
  await mock.install(page);
  let loginUrl = '';
  await page.route('**/auth/github/login**', (route) => {
    loginUrl = route.request().url();
    return route.fulfill({
      status: 200,
      contentType: 'text/html',
      body: '<h1>GitHub</h1>',
    });
  });
  await page.goto('/setup/machines/m1');
  await expect(page).toHaveURL(/\/sign-in$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Sign in' })).toBeVisible();
  await page.getByRole('button', { name: 'Sign in with GitHub' }).click();
  await expect.poll(() => loginUrl).toContain('return_to=%2Fsetup%2Fmachines%2Fm1');
});

test('after sign-in the deep link opens its screen with the session shell', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.goto('/setup/machines/m1');
  await expect(page.getByRole('heading', { level: 1, name: 'build-1' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Areas' })).toBeVisible();
});

test('an unknown path shows not found with a link to the overview', async ({ page }) => {
  await signedIn().install(page);
  await page.goto('/no/such/page');
  await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible();
  await page.getByRole('link', { name: 'Open the overview' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible();
});

test('a user with no access gets the no-access page and no data', async ({ page }) => {
  const mock = signedIn().on(
    'GET',
    '/api/v1/me',
    failure(403, 'forbidden', 'No access.', [{ id: 'stranger' }]),
  );
  await mock.install(page);
  await page.goto('/');
  await expect(page.getByText(/Signed in as stranger/)).toBeVisible();
  expect(mock.count('GET', '/api/v1/machines')).toBe(0);
});
