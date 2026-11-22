// CRSP164 (design system UX-4, UX-12, UX-15, UX-16, UX-17, UX-49; look Option B): every route of the console, at
// 360, 393 and 1300 px, in the dark and the light mode, passes the layout checks and the new DOM checks. The routes
// come from routes.ts, so a new screen is covered without editing this file. A case that today's main fails is a
// test.fixme naming its owner in ux-fixme.ts; there is no known list to add to.
import { expect, test } from '@playwright/test';
import { judgeLayout, measureDomLayout } from '../../../scripts/design/dom-layout';
import { consoleRoutes, NOT_FOUND_URL } from './routes';
import { fixmeFor } from './ux-fixme';
import {
  MODES,
  frames,
  headingShown,
  measureEverywhere,
  openRoute,
  stickyEverywhere,
  stickyWalk,
  stickyWalkAfterChange,
  TEXT_WIDTHS,
  type Measure,
  type ScreenState,
} from './ux-guards';

// A tab is a state of a route, not a route of its own: the Registry tab (REG5) is checked like a screen.
const TABS = [
  {
    pattern: '/setup/images?tab=registry',
    url: '/setup/images?tab=registry',
    signedOut: false,
  },
];

// Every case opens its own page against the shared read-only server, so the cases run in parallel and Playwright
// splits them across shards (one serial file put all of them on one shard).
test.describe.configure({ mode: 'parallel' });

const routes = [
  ...consoleRoutes(),
  ...TABS,
  { pattern: 'not found', url: NOT_FOUND_URL, signedOut: false },
];

const CHECKS: { id: Measure; states: ScreenState[] }[] = [
  { id: 'touch', states: ['data'] },
  { id: 'primary', states: ['data'] },
  { id: 'h1', states: ['data', 'empty', 'failed', 'loading'] },
  { id: 'selectAll', states: ['data'] },
  { id: 'navScroll', states: ['data'] },
  { id: 'noScrollbar', states: ['data'] },
  { id: 'pane', states: ['data'] },
  { id: 'frameEdges', states: ['data'] },
  { id: 'pageEdges', states: ['data'] },
  { id: 'tabEdges', states: ['data'] },
  { id: 'tabGap', states: ['data'] },
  { id: 'navBox', states: ['data'] },
  { id: 'tableHead', states: ['data'] },
  { id: 'paneButtons', states: ['data'] },
  { id: 'titleOverlap', states: ['data'] },
  { id: 'hintKeys', states: ['data'] },
];

// LOOK35 (RES21 T8): the measures of text lines and boxes, on the default data and on the stress data (six machines
// with a long host name, seven runners with a long reason, a repository with many policies), at 360, 393 and 1300 px.
const TEXT_CHECKS: Measure[] = [
  'wrapped',
  'wordBroken',
  'textOverText',
  'textOverBorder',
  'wordsGlued',
  'cutText',
  'rowHeights',
  'rowActionWords',
];
const TEXT_STATES: ScreenState[] = ['data', 'stress'];
// Empty states have their own words and links (RES21 B13, B17): the measures of words and wraps read them too.
const EMPTY_CHECKS: Measure[] = ['wrapped', 'wordBroken', 'wordsGlued'];

for (const route of routes) {
  for (const mode of MODES) {
    test(`ux layout: ${route.pattern} (${mode})`, async ({ page }) => {
      const why = fixmeFor('layout', route.pattern, 'data');
      test.fixme(why !== undefined, why ?? '');
      await openRoute(page, route, { mode });
      await headingShown(page);
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
      const { unknown } = judgeLayout(`${route.pattern} ${mode}`, problems, []);
      expect(unknown).toEqual([]);
    });
  }

  for (const check of CHECKS) {
    for (const state of check.states) {
      test(`ux ${check.id}: ${route.pattern} (${state})`, async ({ page }) => {
        const why = fixmeFor(check.id, route.pattern, state);
        test.fixme(why !== undefined, why ?? '');
        expect(await measureEverywhere(page, route, check.id, state)).toEqual([]);
      });
    }
  }

  for (const id of TEXT_CHECKS) {
    for (const state of TEXT_STATES) {
      test(`ux ${id}: ${route.pattern} (${state})`, async ({ page }) => {
        const why = fixmeFor(id, route.pattern, state);
        test.fixme(why !== undefined, why ?? '');
        expect(await measureEverywhere(page, route, id, state, TEXT_WIDTHS)).toEqual([]);
      });
    }
  }

  for (const id of EMPTY_CHECKS) {
    test(`ux ${id}: ${route.pattern} (empty)`, async ({ page }) => {
      const why = fixmeFor(id, route.pattern, 'empty');
      test.fixme(why !== undefined, why ?? '');
      expect(await measureEverywhere(page, route, id, 'empty', TEXT_WIDTHS)).toEqual([]);
    });
  }

  for (const state of TEXT_STATES) {
    test(`ux stickyWalk: ${route.pattern} (${state})`, async ({ page }) => {
      const why = fixmeFor('stickyWalk', route.pattern, state);
      test.fixme(why !== undefined, why ?? '');
      expect(await stickyEverywhere(page, route, state)).toEqual([]);
    });
  }
}

// RES21 B02: an edit screen shows its Save bar only after a change; the bar must be in the window then too.
for (const route of routes) {
  test(`ux stickyWalk after a change: ${route.pattern}`, async ({ page }) => {
    const why = fixmeFor('stickyEdit', route.pattern, 'stress');
    test.fixme(why !== undefined, why ?? '');
    expect(
      await stickyEverywhere(page, route, 'stress', TEXT_WIDTHS, stickyWalkAfterChange),
    ).toEqual([]);
  });
}

// N5 (RES21): a frame with no key-hint bar (the setup page) has no `--ds-keyhints-height`; the bar still sticks.
// A short window makes the page scroll, so the walk has something to judge.
for (const url of ['/setup?wizard=1', '/update']) {
  for (const mode of MODES) {
    for (const w of TEXT_WIDTHS) {
      test(`ux stickyWalk: ${url} in a short window (${mode}, ${w.width}px)`, async ({
        page,
      }) => {
        const why = fixmeFor('stickyWalk', `${url} short`, 'data');
        test.fixme(why !== undefined, why ?? '');
        await openRoute(page, { url, signedOut: false }, { mode });
        await headingShown(page);
        await page.setViewportSize({ width: w.width, height: 320 });
        await frames(page);
        const found = (await stickyWalk(page)).map(
          (line) => `${url} ${mode} ${w.width}x320: ${line}`,
        );
        expect(found).toEqual([]);
      });
    }
  }
}
