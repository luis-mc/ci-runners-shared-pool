// LOOK33: the block caret of the Phosphor look in the Machines search box and the command palette. The block is
// drawn by the shared text box (CaretInput). It is there while the box has focus, sits at the insertion point,
// follows typing and the arrow keys, and is gone on blur. The screenshots of docs/review/LOOK33 come from the
// last test (LOOK33_SHOTS=1).
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Locator, type Page } from '@playwright/test';
import { openRoute } from './ux-guards';

const CARET = '[data-caret-block]';

/** The x the block should have: the box's left edge, border and padding, plus the width of the text before it. */
async function expectedX(box: Locator, upTo: number): Promise<number> {
  return box.evaluate((el, n) => {
    const input = el as HTMLInputElement;
    const cs = getComputedStyle(input);
    const c = document.createElement('canvas').getContext('2d');
    if (!c) throw new Error('no canvas');
    c.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
    const w = c.measureText(input.value.slice(0, n)).width;
    const r = input.getBoundingClientRect();
    return r.left + parseFloat(cs.borderLeftWidth) + parseFloat(cs.paddingLeft) + w;
  }, upTo);
}

async function caretX(page: Page): Promise<number> {
  const box = await page.locator(CARET).boundingBox();
  if (!box) throw new Error('the block caret is not shown');
  return box.x;
}

async function check(page: Page, box: Locator): Promise<void> {
  await box.blur();
  await expect(page.locator(CARET)).toHaveCount(0);
  await box.focus();
  await expect(page.locator(CARET)).toBeVisible();
  await expect(page.locator(CARET)).toHaveAttribute('aria-hidden', 'true');
  expect(await box.evaluate((e) => getComputedStyle(e).caretColor)).toBe(
    'rgba(0, 0, 0, 0)',
  );
  await page.keyboard.type('asd');
  expect(Math.abs((await caretX(page)) - (await expectedX(box, 3)))).toBeLessThan(2);
  await page.keyboard.press('ArrowLeft');
  expect(Math.abs((await caretX(page)) - (await expectedX(box, 2)))).toBeLessThan(2);
  await page.keyboard.press('Home');
  expect(Math.abs((await caretX(page)) - (await expectedX(box, 0)))).toBeLessThan(2);
  await page.keyboard.press('End');
  expect(Math.abs((await caretX(page)) - (await expectedX(box, 3)))).toBeLessThan(2);
  await page.keyboard.press('Shift+ArrowLeft');
  await expect(page.locator(CARET)).toHaveCount(0);
  await page.keyboard.press('End');
  await expect(page.locator(CARET)).toBeVisible();
  await box.blur();
  await expect(page.locator(CARET)).toHaveCount(0);
}

test('Machines search: the block caret follows the text and goes on blur', async ({
  page,
}) => {
  await openRoute(page, { url: '/setup/machines', signedOut: false });
  await check(page, page.getByRole('textbox', { name: 'Search machines' }));
});

test('Command palette: the block caret follows the text and goes on blur', async ({
  page,
}) => {
  await openRoute(page, { url: '/', signedOut: false });
  await page.keyboard.press('Control+K');
  const dialog = page.getByRole('dialog', { name: 'Commands' });
  await expect(dialog.getByRole('textbox')).toBeFocused();
  await check(page, dialog.getByRole('textbox'));
});

test('the block is steady under reduced motion and blinks otherwise', async ({
  page,
}) => {
  await openRoute(page, { url: '/setup/machines', signedOut: false });
  await page.getByRole('textbox', { name: 'Search machines' }).focus();
  const name = () =>
    page.locator(CARET).evaluate((e) => getComputedStyle(e).animationName);
  expect(await name()).toBe('ds-caret-blink');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  expect(await name()).toBe('none');
});

test('screenshots', async ({ page }) => {
  test.skip(!process.env.LOOK33_SHOTS, 'only to make docs/review/LOOK33');
  const dir = resolve('docs/review/LOOK33');
  mkdirSync(dir, { recursive: true });
  const cases = [
    { theme: 'phosphor', mode: 'dark' as const },
    { theme: 'gruvbox', mode: 'light' as const },
  ];
  for (const c of cases) {
    for (const where of ['machines', 'palette']) {
      await openRoute(
        page,
        { url: where === 'machines' ? '/setup/machines' : '/', signedOut: false },
        { mode: c.mode, themeName: c.theme },
      );
      let box: Locator;
      let shot: Locator;
      if (where === 'machines') {
        box = page.getByRole('textbox', { name: 'Search machines' });
        shot = page.locator('main').first();
      } else {
        await page.keyboard.press('Control+K');
        shot = page.getByRole('dialog', { name: 'Commands' });
        box = shot.getByRole('textbox');
      }
      await box.focus();
      await page.keyboard.type('asd');
      await page.keyboard.press('ArrowLeft');
      for (const frame of ['on', 'off']) {
        // Hold the blink at one phase.
        await page.locator(CARET).evaluate((e, on) => {
          e.style.animation = 'none';
          e.style.opacity = on ? '1' : '0';
        }, frame === 'on');
        await shot.screenshot({
          path: resolve(dir, `${where}-${c.theme}-${c.mode}-${frame}.png`),
        });
      }
    }
  }
});
