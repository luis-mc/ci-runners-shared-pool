// LOOK66 (web-console R5.167, R5.168; design system CORE-1 to CORE-7, UX-1, UX-17, UX-29): Pool > Machines with the
// table Runners on {machine} in each card, and Analytics > Insights with the Waits table and the Runner timeline, at
// 360, 393 and 1300 px in both modes. Shared parts only, so the DOM layout checks and the text measures find nothing.
import { expect, test } from '@playwright/test';
import { judgeLayout, measureDomLayout } from '../../../scripts/design/dom-layout';
import { insightsJobs, machine, runner, signedIn } from './fixtures';
import {
  MODES,
  TEXT_WIDTHS,
  frames,
  headingShown,
  measure,
  settle,
  type Measure,
} from './ux-guards';

const TEXT: Measure[] = [
  'wrapped',
  'wordBroken',
  'textOverText',
  'textOverBorder',
  'wordsGlued',
  'cutText',
  'noScrollbar',
  'pane',
];

const runners = [
  runner({ id: 'r1', name: 'gameroompc-pool-1', size_class: 'medium' }),
  runner({ id: 'r2', name: 'gameroompc-pool-2', kind: 'ephemeral', size_class: 'small' }),
  runner({ id: 'r3', name: 'gameroompc-pool-3', size_class: 'large' }),
  runner({ id: 'r4', name: 'gameroompc-pool-4' }),
];

for (const mode of MODES) {
  for (const w of TEXT_WIDTHS) {
    for (const [name, path, region] of [
      ['Pool > Machines', '/pool/machines', 'build-1'],
      ['Analytics > Insights', '/analytics/insights', 'Waits'],
    ] as const) {
      test(`${name} at ${w.width} px (${mode})`, async ({ page }) => {
        const mock = insightsJobs(signedIn())
          .on('GET', '/api/v1/me/preferences', { theme: mode })
          .on('GET', '/api/v1/machines', { items: [machine()] })
          .on('GET', '/api/v1/runners', { items: runners });
        await mock.install(page);
        await page.emulateMedia({ colorScheme: mode });
        await page.setViewportSize(w);
        await page.goto(path);
        await settle(page, mode);
        await headingShown(page);
        await expect(
          page.getByRole('region', { name: region, exact: true }),
        ).toBeVisible();
        if (name === 'Pool > Machines') {
          const card = page.getByRole('region', { name: 'build-1', exact: true });
          await expect(card.getByText('gameroompc-pool-4')).toBeVisible();
          await expect(card.getByText('No runner is on this machine.')).toHaveCount(0);
          await expect(
            page.getByRole('region', { name: 'Runners', exact: true }),
          ).toHaveCount(0);
        } else {
          const waits = page.getByRole('region', { name: 'Waits' });
          await expect(waits.getByText('crsp-build-c4a7')).toBeVisible();
          await expect(
            page.getByRole('heading', { name: 'Runner timeline' }),
          ).toBeVisible();
        }
        await frames(page);
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
        ).toBe(true);
        const problems = await measureDomLayout(page, {
          known: [],
          spaceVar: '--ds-space-',
          sectionGapVar: '--ds-space-6',
          focusVars: {
            width: '--ds-focus-width',
            offset: '--ds-focus-offset',
            glow: '--ds-glow-focus',
          },
          ready: () => frames(page),
        });
        expect(judgeLayout(`${name} ${w.width} ${mode}`, problems, []).unknown).toEqual(
          [],
        );
        for (const id of TEXT) expect(await measure(page, id), id).toEqual([]);
      });
    }
  }
}
