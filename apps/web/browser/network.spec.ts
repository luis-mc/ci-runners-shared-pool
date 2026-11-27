// T12 (R6.5, R6.6): no third-party request, no cookie, and the security headers on every response.
import { expect, test } from '@playwright/test';
import { PAGES } from './pages.ts';

for (const path of PAGES) {
  test(`${path} makes only same-origin requests and sets no cookie`, async ({
    page,
    baseURL,
  }) => {
    const origin = new URL(baseURL!).origin;
    const foreign: string[] = [];
    const responses: { url: string; headers: Record<string, string> }[] = [];
    page.on('request', (r) => {
      if (!r.url().startsWith(origin) && !r.url().startsWith('data:'))
        foreign.push(r.url());
    });
    page.on('response', (r) => {
      if (r.url().startsWith(origin) && r.request().resourceType() === 'document')
        responses.push({ url: r.url(), headers: r.headers() });
    });
    await page.goto(path);
    await page.waitForLoadState('networkidle');
    expect(foreign).toEqual([]);
    expect(await page.context().cookies()).toEqual([]);
    expect(await page.evaluate(() => document.cookie)).toBe('');
    expect(responses.length).toBeGreaterThan(0);
    for (const { headers } of responses) {
      const csp = headers['content-security-policy'] ?? '';
      expect(csp).toContain("default-src 'none'");
      expect(csp).toContain("script-src 'self'");
      expect(csp).not.toContain('unsafe-inline');
      expect(headers['strict-transport-security']).toContain('max-age=');
      expect(headers['referrer-policy']).toBe('strict-origin-when-cross-origin');
      expect(headers['x-content-type-options']).toBe('nosniff');
    }
  });
}

test('the strict policy blocks nothing the page needs', async ({ page }) => {
  const violations: string[] = [];
  page.on('console', (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) violations.push(m.text());
  });
  for (const path of ['/', '/docs/features/website', '/docs']) {
    await page.goto(path);
    await page.waitForLoadState('networkidle');
  }
  expect(violations).toEqual([]);
});
