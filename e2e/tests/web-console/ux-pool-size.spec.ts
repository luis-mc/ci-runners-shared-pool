// OPS61 (machines R5.68; design system CORE-1 to CORE-7, UX-4, UX-10, UX-17, UX-37): the Pool size block of a machine
// that runs its pool in WSL2, with a measured basis, a Custom size under the PC's own use and the .wslconfig lines,
// at 360, 393 and 1300 px, in both modes. The block uses shared parts only, so the DOM layout checks and the text
// measures find nothing, and the page does not scroll sideways.
import { expect, test } from '@playwright/test';
import { judgeLayout, measureDomLayout } from '../../../scripts/design/dom-layout';
import { machine, signedIn } from './fixtures';
import {
  MODES,
  TEXT_WIDTHS,
  frames,
  headingShown,
  measure,
  settle,
  type Measure,
} from './ux-guards';

const check = {
  known: true,
  ok: true,
  needs_shrink_confirm: false,
  nearest: [],
  total_gib: 32,
  used_outside_gib: 8,
  memory_budget_gib: 27.2,
  room_gib: 25,
  preset_room_gib: 16,
  safety_margin_gib: 1,
  cpu_budget: 4,
  decided: 'fits',
  basis: 'measured',
  host_p95_gib: 10.2,
  host_span_hours: 170,
  measured_room_gib: 16,
  leaves_gib: 8,
  leaves_less_than_host: true,
  wsl_pool: true,
  wsl_cap_set: true,
  wsl_cap_gib: 28,
  wsl_cap_above_pc: true,
  current_memory_gib: 12,
  current_cpus: 4,
  recommended_half: { memory_gib: 8, cpus: 2 },
  recommended_three_quarters: { memory_gib: 12, cpus: 3 },
};

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

for (const mode of MODES) {
  for (const w of TEXT_WIDTHS) {
    test(`pool size block at ${w.width} px (${mode})`, async ({ page }) => {
      const win = machine({
        os: 'windows',
        settings: { ...machine().settings, pool_memory_gib: 12, pool_cpus: 4 },
      });
      const mock = signedIn()
        .on('GET', '/api/v1/me/preferences', { theme: mode })
        .on('GET', '/api/v1/machines/:id', win)
        .on('POST', '/api/v1/machines/:id/pool-size-check', check);
      await mock.install(page);
      await page.emulateMedia({ colorScheme: mode });
      await page.setViewportSize(w);
      await page.goto('/pool/machines/m1');
      await settle(page, mode);
      await headingShown(page);
      const section = page.getByRole('region', { name: 'Pool size' });
      await expect(
        section.getByText('Windows and your apps use up to 10.2 GiB (p95, 7 days)'),
      ).toBeVisible();
      await expect(section.getByText('WSL memory cap now')).toBeVisible();
      await expect(section.getByRole('button', { name: 'Copy' })).toBeVisible();
      // The warning for a Custom size under the PC's own use.
      await section.getByRole('radio', { name: /Custom/ }).check();
      await section.getByRole('button', { name: /Pool memory/ }).click();
      const keypad = page.getByRole('dialog');
      for (const k of ['2', '4'])
        await keypad.getByRole('button', { name: k, exact: true }).click();
      await keypad.getByRole('button', { name: 'Done' }).click();
      await expect(section.getByText(/leaves 8 GiB for this PC/)).toBeVisible();
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
      expect(judgeLayout(`pool size ${w.width} ${mode}`, problems, []).unknown).toEqual(
        [],
      );
      for (const id of TEXT) expect(await measure(page, id), id).toEqual([]);
    });
  }
}
