// LOOK44, LOOK47, LOOK48: the Help page (layout G, D58). The job path is on top, every question card is shown at once
// below it in the order of its box, with a badge and a small picture, nothing is locked, a ? beside a term opens a short answer that links to the same card, and Down, Up, j and k move
// between the controls. Layout at
// 360, 393 and 1300 px is checked for every route by ux-routes.spec.ts; this file checks the behaviour.
import { expect, test } from '@playwright/test';
import { machine, signedIn } from './fixtures';

const real = machine({
  settings: {
    budget_memory_pct: 70,
    budget_cpu_pct: 55,
    job_slots: 3,
    light_slots: 1,
    heavy_slots: 2,
    allowed_backends: ['docker'],
    drain_timeout_minutes: 30,
    windows_symlink_right: false,
  },
});

test('Help is the seventh item of the menu and shows the picture and every card', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.goto('/');
  const nav = page.getByRole('navigation', { name: 'Areas' });
  await expect(nav.getByRole('link')).toHaveCount(7);
  await nav.getByRole('link', { name: 'Help' }).click();
  await expect(page).toHaveURL(/\/help$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Help' })).toBeVisible();
  // No wizard: no step bar, no Back, Next or Start again.
  await expect(page.getByRole('list', { name: 'Steps' })).toHaveCount(0);
  for (const name of ['Back', 'Next', 'Start again']) {
    await expect(page.getByRole('button', { name })).toHaveCount(0);
  }
  const picture = page.getByRole('region', { name: 'How a job gets run' });
  const questions = page.getByRole('region', {
    name: 'Questions, in the order a job meets them',
  });
  await expect(picture).toBeVisible();
  // The cards of all three parts are in the page together, in the grid below the picture.
  for (const title of [
    'What does shared mean?',
    'What is a slot?',
    'What is the Doctor?',
    'What is the tray app?',
    'What does Run elsewhere mean?',
  ]) {
    await expect(questions.getByRole('heading', { name: title })).toBeAttached();
  }
  await expect(
    picture.getByRole('heading', { name: 'What does Run elsewhere mean?' }),
  ).toHaveCount(0);
  // Nothing is locked: every box of the picture can be pressed.
  await expect(picture.locator('button:disabled')).toHaveCount(0);
});

test('pressing a part of the picture brings its card into view', async ({ page }) => {
  await signedIn().install(page);
  await page.setViewportSize({ width: 1300, height: 700 });
  await page.goto('/help');
  await page.getByRole('button', { name: /^Slot/ }).click();
  await expect(page.getByRole('heading', { name: 'What is a slot?' })).toBeInViewport();
});

test('the pictures show the real slots and load budget of the machine', async ({
  page,
}) => {
  await signedIn()
    .on('GET', '/api/v1/machines', { items: [real] })
    .install(page);
  await page.goto('/help#q-slot');
  await expect(page.getByText('1 of 3 slots busy')).toBeVisible();
  await expect(page.getByText(/Pool size makes slots/)).toBeVisible();
  await expect(page.getByText(/^Memory \d+% of 70%$/)).toBeAttached();
  await expect(page.getByText(/^CPU \d+% of 55%$/)).toBeAttached();
  await expect(page.getByText(/85%/)).toHaveCount(0);
});

test('the intro, the note and each card badge are there, and a badge lights its box', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.setViewportSize({ width: 1300, height: 900 });
  await page.goto('/help');
  await expect(
    page.getByText(
      'One picture, then the questions people ask. Each card names its box.',
    ),
  ).toBeVisible();
  await expect(
    page.getByText('1 to 3 happen on GitHub. 4 to 7 happen in your pool.'),
  ).toBeVisible();
  const card = page.getByRole('region', { name: 'What is a slot?' });
  await expect(card.getByRole('link', { name: 'Box 5: Slot' })).toBeVisible();
  await expect(card.getByRole('img')).toBeVisible();
  await card.getByRole('link', { name: 'Box 5: Slot' }).click();
  await expect(page.getByRole('button', { name: /^Slot/ })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
});

test('the cards sit in four columns at 1300 px, two on a tablet and one on a phone', async ({
  page,
}) => {
  await signedIn().install(page);
  const columns = async (): Promise<number> => {
    const tops = await page
      .locator('.ds-card-grid > *')
      .evaluateAll((els) => els.map((c) => Math.round(c.getBoundingClientRect().top)));
    return tops.filter((t) => t === tops[0]).length;
  };
  for (const [width, expected] of [
    [1300, 4],
    [768, 2],
    [393, 1],
    [360, 1],
  ] as const) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/help');
    await expect(page.getByRole('heading', { name: 'What is a slot?' })).toBeVisible();
    expect(await columns()).toBe(expected);
  }
});

test('the ? beside Slots opens a short answer that links to its Help card', async ({
  page,
}) => {
  await signedIn().on('GET', '/api/v1/machines/:id', real).install(page);
  await page.goto('/pool/machines/m1');
  await page.getByRole('button', { name: 'What does Slots mean?' }).click();
  const panel = page.getByRole('group', { name: 'Slots' });
  await expect(panel).toBeVisible();
  await panel.getByRole('link', { name: 'More about this in Help' }).click();
  await expect(page).toHaveURL(/\/help#q-slot$/);
  await expect(page.getByRole('heading', { name: 'What is a slot?' })).toBeVisible();
});

test('Escape closes the ? answer', async ({ page }) => {
  await signedIn().install(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'What does Queue mean?' }).click();
  await expect(page.getByRole('group', { name: 'Queue' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('group', { name: 'Queue' })).toHaveCount(0);
});

test('Down and j reach the boxes of the picture, Up and k come back', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.goto('/help');
  const repo = page.getByRole('button', { name: /^Repository/ });
  await repo.focus();
  await page.keyboard.press('j');
  const run = page.getByRole('button', { name: /^Run(?!ner)/ });
  await expect(run).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('button', { name: /^Job/ })).toBeFocused();
  await page.keyboard.press('k');
  await expect(run).toBeFocused();
});

test('the first-run Overview shows the picture and Open Help', async ({ page }) => {
  await signedIn()
    .on('GET', '/api/v1/machines', { items: [] })
    .on('GET', '/api/v1/runners', { items: [] })
    .install(page);
  await page.goto('/');
  const picture = page.getByRole('region', { name: 'How a job gets run' });
  await expect(picture).toBeVisible();
  await picture.getByRole('button', { name: 'Open Help' }).click();
  await expect(page).toHaveURL(/\/help$/);
});

for (const width of [360, 393, 1300]) {
  test(`Help does not scroll sideways at ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await signedIn().install(page);
    await page.goto('/help');
    await expect(page.getByRole('heading', { level: 1, name: 'Help' })).toBeVisible();
    const over = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(over).toBeLessThanOrEqual(0);
  });
}
