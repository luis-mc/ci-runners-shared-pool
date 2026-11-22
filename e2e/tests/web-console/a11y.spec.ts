// R6.6: every console screen, in the dark and the light theme, has no serious or critical axe violation, one h1,
// a main landmark and a visible focus. The screens come from the route table (routes.ts), so a new screen is covered
// without editing this file. No known-issues list: a violation is fixed.
import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { failure, needingRecovery, publicRows, signedIn } from './fixtures';
import { jobsAndDoctor } from './jobs-doctor-fixtures';
import { consoleRoutes, NOT_FOUND_URL } from './routes';

const screens = [
  ...consoleRoutes().map((r) => ({
    name: r.pattern,
    url: r.url,
    signedOut: r.signedOut,
    recovery: false,
  })),
  { name: 'not found', url: NOT_FOUND_URL, signedOut: false, recovery: false },
  // The state of an agent that needs recovery (web-console R5.33): the page and the list.
  {
    name: 'machine detail needing recovery',
    url: '/setup/machines/m1',
    signedOut: false,
    recovery: true,
  },
  {
    name: 'jobs empty',
    url: '/pool',
    signedOut: false,
    recovery: false,
    emptyJobs: true,
  },
  {
    name: 'repositories with public rows while the setting is off',
    url: '/setup/pool',
    signedOut: false,
    recovery: false,
    publicRows: true,
  },
  // The GitHub grant part (github-integration R5.86, web-console R5.93): one step list and one primary action.
  {
    name: 'repositories with a missing App permission (the GitHub grant)',
    url: '/setup/pool',
    signedOut: false,
    recovery: false,
    grant: true,
  },
  {
    name: 'machines needing recovery',
    url: '/setup/machines',
    signedOut: false,
    recovery: true,
  },
];

for (const theme of ['dark', 'light'] as const) {
  for (const screen of screens) {
    test(`a11y ${theme}: ${screen.name}`, async ({ page }) => {
      const mock = jobsAndDoctor(signedIn());
      mock.on('GET', '/api/v1/me/preferences', { theme });
      if (screen.recovery)
        mock
          .on('GET', '/api/v1/machines/:id', needingRecovery())
          .on('GET', '/api/v1/machines', { items: [needingRecovery()] });
      if ('publicRows' in screen)
        mock.on('GET', '/api/v1/github/repositories', publicRows());
      if ('grant' in screen)
        mock
          .on('GET', '/api/v1/github/installations', {
            items: [
              {
                id: 10,
                account_login: 'octo',
                account_type: 'User',
                suspended: false,
                repository_selection: 'selected',
                missing_permissions: ['secrets'],
              },
            ],
            stale: { stale: false },
          })
          .on('GET', '/api/v1/github/grant', {
            ready: false,
            next: 'accept',
            missing: ['secrets'],
            installation_id: 10,
            steps: [
              { id: 'install', done: true, permissions: [] },
              { id: 'ask', done: true, permissions: [] },
              {
                id: 'accept',
                done: false,
                permissions: ['secrets'],
                url: 'https://github.com/settings/installations/10',
              },
            ],
          });
      if ('emptyJobs' in screen)
        mock.on('GET', '/api/v1/jobs', { items: [], stale: { stale: false } });
      if (screen.signedOut)
        mock.on('GET', '/api/v1/me', failure(401, 'unauthenticated', 'Sign in.'));
      await mock.install(page);
      await page.emulateMedia({ colorScheme: theme });
      await page.goto(screen.url);
      await expect(page.locator('h1')).toHaveCount(1);
      await expect(page.locator('h1')).toBeVisible();
      await page.waitForLoadState('networkidle');
      // Sign-in and first run have no stored theme: set it the way the appearance setting does (UX-60).
      await page.evaluate(
        (t) => document.documentElement.setAttribute('data-theme', t),
        theme,
      );
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme);

      await expect(page.getByRole('main')).toHaveCount(1);

      const result = await new AxeBuilder({ page }).analyze();
      const bad = result.violations.filter(
        (v) => v.impact === 'serious' || v.impact === 'critical',
      );
      expect(
        bad.map((v) => ({
          id: v.id,
          impact: v.impact,
          help: v.help,
          nodes: v.nodes
            .slice(0, 5)
            .map((n) => ({ html: n.html, why: n.failureSummary })),
        })),
      ).toEqual([]);

      // A visible focus: the first Tab lands on a control that draws an outline or a ring (UX-17, UX-59).
      // A screen with nothing to press (the first-run page when every step is done) has no focus to see.
      const tabbable = await page
        .locator(
          'a[href], button:not([disabled]), input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])',
        )
        .count();
      if (tabbable === 0) return;
      await page.keyboard.press('Tab');
      const focus = await page.evaluate(() => {
        const el = document.activeElement;
        if (!el || el === document.body) return null;
        const s = getComputedStyle(el);
        return {
          tag: el.tagName,
          outline: s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) > 0,
          ring: s.boxShadow !== 'none',
        };
      });
      expect(focus, 'the first Tab must land on a control').not.toBeNull();
      expect(focus?.outline || focus?.ring, `focus ring on ${focus?.tag}`).toBe(true);
    });
  }
}

// R6.12 (github-integration): on the wizard, sign-in, repositories and users screens the Tab key walks the controls
// in the order they are read, each one visible, and no control sets a positive tabindex (UX-17).
const focusScreens = [
  { name: 'sign-in', url: '/sign-in', signedOut: true },
  { name: 'first-run wizard', url: '/setup?wizard=1', signedOut: false },
  { name: 'repositories', url: '/setup/pool', signedOut: false },
  { name: 'users', url: '/settings/access', signedOut: false },
];

for (const screen of focusScreens) {
  test(`focus order: ${screen.name}`, async ({ page }) => {
    const mock = jobsAndDoctor(signedIn());
    if (screen.signedOut)
      mock.on('GET', '/api/v1/me', failure(401, 'unauthenticated', 'Sign in.'));
    await mock.install(page);
    await page.goto(screen.url);
    await expect(page.locator('h1')).toBeVisible();
    await page.waitForLoadState('networkidle');
    expect(
      await page.locator('[tabindex]:not([tabindex="0"]):not([tabindex^="-"])').count(),
    ).toBe(0);
    const stops: { pos: number; visible: boolean; tag: string }[] = [];
    for (let i = 0; i < 40; i++) {
      await page.keyboard.press('Tab');
      const stop = await page.evaluate(() => {
        const el = document.activeElement;
        if (!el || el === document.body) return null;
        const all = Array.from(document.querySelectorAll('*'));
        const r = el.getBoundingClientRect();
        return {
          pos: all.indexOf(el),
          visible: r.width > 0 && r.height > 0,
          tag: el.tagName,
        };
      });
      if (!stop) break;
      if (stops.length > 0 && stop.pos === stops[0]?.pos) break; // wrapped round
      stops.push(stop);
    }
    // A screen with nothing to press (the first-run page when every step is done) has no focus order to judge.
    const pressable = await page
      .locator('a[href], button:not([disabled]), input:not([disabled]), select, textarea')
      .count();
    if (pressable > 0)
      expect(stops.length, 'at least one control takes focus').toBeGreaterThan(0);
    for (const s of stops) expect(s.visible, `${s.tag} is visible`).toBe(true);
    const order = stops.map((s) => s.pos);
    expect(order, 'Tab follows the reading order').toEqual(
      [...order].sort((a, b) => a - b),
    );
  });
}
