// Shared parts of the UX guard specs (CRSP164): open any console route in a state, theme and width, and measure
// the DOM facts the design-system rules name. Every measure runs in the page and returns plain problem lines;
// a spec asserts the list is empty. No screenshots.
import type { Page } from '@playwright/test';
import { failure, machine, repository, runner, signedIn, type Mock } from './fixtures';
import { jobsAndDoctor } from './jobs-doctor-fixtures';
import type { ConsoleRoute } from './routes';
import {
  rowActionWords,
  rowActionWordsMeasure,
  TEXT_MEASURES,
  VISIBLE,
} from './ux-measure-source';

export const WIDTHS = [
  { width: 360, height: 780 },
  { width: 393, height: 852 },
  { width: 1300, height: 900 },
  { width: 1800, height: 1000 },
] as const;
export const MODES = ['dark', 'light'] as const;
export type Mode = (typeof MODES)[number];
/** `stress` is the data state with the stress data set (see `stressData`): long names, failed and offline machines. `loading` never answers a data call, so the page stays in its loading state. */
export type ScreenState = 'data' | 'empty' | 'failed' | 'stress' | 'loading';
/** The widths the text measures (LOOK35) run at: phone, large phone, desktop. */
export const TEXT_WIDTHS = WIDTHS.filter((w) => w.width !== 1800);

const SESSION = ['/api/v1/me', '/api/v1/me/preferences', '/api/v1/events'];

/** The collections a list screen reads; "empty" answers them with no rows. */
const LISTS = [
  '/api/v1/machines',
  '/api/v1/runners',
  '/api/v1/images',
  '/api/v1/registry/images',
  '/api/v1/caches',
  '/api/v1/users',
  '/api/v1/jobs',
  '/api/v1/runs',
  '/api/v1/jobs/groups',
  '/api/v1/github/repositories',
  '/api/v1/doctor/findings',
];

const LONG_HOST = 'mac-studio-ci-runner-with-a-very-long-hostname-01.local';
const LONG_REASON =
  'no usable backend: docker is not running (cannot connect to the Docker daemon at unix:///var/run/docker.sock) and persistent is not allowed on this machine';
const STRESS_MACHINES = [
  machine(),
  machine({
    id: 'm2',
    name: 'GameRoomPC',
    os: 'windows',
    container_os: 'linux',
    slots_used: 3,
    load: {
      memory_pct: 91,
      cpu_pct: 88,
      memory_known: true,
      cpu_known: true,
      sampled_at: '2026-11-14T10:00:00Z',
      stale: false,
    },
  }),
  machine({
    id: 'm3',
    name: LONG_HOST,
    os: 'darwin',
    arch: 'arm64',
    state: 'drained',
    accepting: false,
    taking: { taking_jobs: false, reason: 'stopped' },
  }),
  machine({
    id: 'm4',
    name: 'offline-box',
    online: false,
    state: 'offline',
    last_seen_at: '2026-11-10T10:00:00Z',
  }),
  machine({ id: 'm5', name: 'controller-nuc', is_controller: true }),
  machine({
    id: 'm6',
    name: 'old-agent',
    recovery: {
      state: 'needs_recovery',
      reason: 'old_protocol',
      has: '0.1.0',
      needs: '0.3.0',
      since: '2026-11-14T08:00:00Z',
      armed: false,
    },
  }),
];
const STRESS_RUNNERS = [
  runner(),
  runner({
    id: 'r2',
    name: 'crsp-linux-2',
    state: 'busy',
    current_job: {
      id: 9,
      repository: 'octo/app',
      run_id: 1,
      run_attempt: 1,
      github_job_id: 1,
      job_name: 'e2e',
      labels: [],
      state: 'running',
    },
  }),
  runner({
    id: 'r3',
    name: 'crsp-linux-3',
    state: 'failed',
    failed_reason: LONG_REASON,
    reason: LONG_REASON,
  }),
  runner({ id: 'r4', name: 'crsp-win-1', os: 'windows', state: 'offline' }),
  runner({
    id: 'r5',
    name: 'crsp-eph-5',
    kind: 'ephemeral',
    backend: 'docker',
    isolation: 'container',
    keeps_state: false,
    state: 'idle',
    managed: false,
  }),
  runner({
    id: 'r6',
    name: 'crsp-mac-6',
    os: 'darwin',
    arch: 'arm64',
    state: 'idle',
    unclean_reason: 'The clean-up of the work folder failed: the folder is in use.',
  }),
  runner({
    id: 'r7',
    name: 'crsp-a-very-long-runner-name-for-the-wrapping-check-7',
    state: 'not_registered',
  }),
];
const MANY_POLICIES = repository({
  policy: {
    priority: 10,
    max_parallel_jobs: 2,
    allow_fork_pull_requests: false,
    labels: [
      'self-hosted',
      'linux',
      'x64',
      'gpu',
      'large-memory',
      'nightly',
      'release',
      'docker',
      'persistent',
      'cache-heavy',
      'long-running-integration-label',
      'arm64',
    ],
    runner_groups: [
      'default',
      'release-builders',
      'nightly',
      'gpu-pool',
      'long-group-name-for-the-wrapping-check',
    ],
  },
  variables: [
    { name: 'RUNS_ON_LINUX', status: 'ok', value: '["self-hosted","linux","x64"]' },
    {
      name: 'RUNS_ON_WINDOWS',
      status: 'wrong',
      value: '["self-hosted","windows","x64","very-long-label-that-goes-on"]',
    },
    { name: 'RUNS_ON_MACOS', status: 'missing' },
    { name: 'RUNS_ON_LIGHT', status: 'not_managed', value: '["ubuntu-latest"]' },
  ],
});

/** The stress data set (LOOK35, RES21): six machines, seven runners, a repository with many policies. */
export function stressData(mock: Mock): Mock {
  mock.on('GET', '/api/v1/machines', { items: STRESS_MACHINES });
  mock.on('GET', '/api/v1/runners', { items: STRESS_RUNNERS });
  mock.on(
    'GET',
    '/api/v1/runners/:id',
    (_c: unknown, p: Record<string, string>) =>
      STRESS_RUNNERS.find((r) => r.id === p.id) ?? STRESS_RUNNERS[0],
  );
  mock.on(
    'GET',
    '/api/v1/machines/:id',
    (_c: unknown, p: Record<string, string>) =>
      STRESS_MACHINES.find((m) => m.id === p.id) ?? STRESS_MACHINES[0],
  );
  mock.on('GET', '/api/v1/overview', {
    machines: STRESS_MACHINES,
    queued_count: 4,
    running: [],
    last_event_id: '1',
  });
  mock.on('GET', '/api/v1/repositories/:id', MANY_POLICIES);
  mock.on('GET', '/api/v1/github/repositories', {
    stale: { stale: false },
    items: [
      MANY_POLICIES,
      repository({
        id: 8,
        full_name: 'octo/another-repository-with-a-long-name',
        name: 'another-repository-with-a-long-name',
      }),
    ],
  });
  return mock;
}

/** Opens a route with the mocked controller. `failed` answers every data call with a 500, `empty` has no rows. */
export async function openRoute(
  page: Page,
  route: Pick<ConsoleRoute, 'url' | 'signedOut'>,
  opts: { mode?: Mode; state?: ScreenState; themeName?: string; width?: number } = {},
): Promise<Mock> {
  const mode = opts.mode ?? 'dark';
  const state = opts.state ?? 'data';
  const mock = jobsAndDoctor(signedIn());
  mock.on('GET', '/api/v1/me/preferences', { theme: mode });
  if (state === 'stress') stressData(mock);
  if (route.signedOut)
    mock.on('GET', '/api/v1/me', failure(401, 'unauthenticated', 'Sign in.'));
  if (state === 'empty') {
    for (const p of LISTS) mock.on('GET', p, { items: [], stale: { stale: false } });
    mock.on('GET', '/api/v1/machines/:id', failure(404, 'not_found', 'No such machine.'));
    mock.on(
      'GET',
      '/api/v1/repositories/:id',
      failure(404, 'not_found', 'No such repository.'),
    );
    mock.on('GET', '/api/v1/jobs/:id', failure(404, 'not_found', 'No such job.'));
    mock.on('GET', '/api/v1/runs/:id', failure(404, 'not_found', 'No such run.'));
  }
  await mock.install(page);
  if (state === 'failed' || state === 'loading')
    await page.route('**/api/v1/**', (r) => {
      const path = new URL(r.request().url()).pathname;
      if (SESSION.includes(path)) return r.fallback();
      if (state === 'loading') return; // never answered: the loading state stays
      return r.fulfill({
        status: 500,
        json: {
          error: { code: 'internal', message: 'The controller could not answer.' },
        },
      });
    });
  await page.emulateMedia({ colorScheme: mode });
  await page.addInitScript(
    ([name, m]) => {
      try {
        if (name) window.localStorage.setItem('crsp.theme-name', name);
        window.localStorage.setItem('crsp.theme-mode', m as string);
      } catch {
        // storage blocked: the page keeps its default
      }
    },
    [opts.themeName ?? '', mode],
  );
  await page.setViewportSize({
    width: opts.width ?? 1300,
    height: WIDTHS.find((w) => w.width === (opts.width ?? 1300))?.height ?? 900,
  });
  await page.goto(route.url);
  await settle(page, mode, state === 'loading');
  return mock;
}

/** Waits for the page to be at rest, and puts the mode on pages that have no stored one (sign-in, first run). */
/** Waits until the page has drawn after a change (a resize, a click): two animation frames, no fixed time. */
export async function frames(page: Page): Promise<void> {
  await page.evaluate(
    () =>
      new Promise<void>((done) =>
        requestAnimationFrame(() => requestAnimationFrame(() => done())),
      ),
  );
}

/** The page's first level heading is on screen: the page has rendered its data (or its empty or failed words). */
export async function headingShown(page: Page): Promise<void> {
  await page.getByRole('heading', { level: 1 }).first().waitFor({ state: 'visible' });
}

export async function settle(page: Page, mode: Mode, loading = false): Promise<void> {
  await page.locator('main').first().waitFor();
  if (loading)
    await frames(page); // never answered: the loading state is at rest once drawn
  else await page.waitForLoadState('networkidle');
  await page.evaluate(
    (m) => document.documentElement.setAttribute('data-theme', m),
    mode,
  );
  await frames(page);
}

/** The measures. Each is a function body run in the page; it returns a list of problem lines. */
const MEASURES = {
  ...TEXT_MEASURES,
  rowActionWords: rowActionWordsMeasure(rowActionWords()),
  /** UX-16: every control has a hit area of at least 44 by 44 px. */
  touch: `${VISIBLE}
    const sel = 'a[href], button, input:not([type=hidden]), select, textarea, summary, [role=button], [role=tab], [role=menuitem], [role=checkbox], [role=switch], [role=radio], [role=option]';
    const bad = [];
    for (const e of document.querySelectorAll(sel)) {
      if (!visible(e)) continue;
      let box = e;
      if (e.matches('input[type=checkbox], input[type=radio]')) { const l = e.closest('label'); if (l && visible(l)) box = l; }
      const r = box.getBoundingClientRect();
      if (r.width <= 2 && r.height <= 2) continue; // a skip link or a hidden control
      if (r.width < 43.5 || r.height < 43.5) bad.push(label(e) + ' is ' + Math.round(r.width) + 'x' + Math.round(r.height) + ' px');
    }
    return bad;`,
  /** UX-4: one primary button on the screen (a screen that offers an action has exactly one). The tabs and
   *  segmented switches that switch the section, the filter chips, the sort buttons of a table head, the row buttons of a table, the boxes of the job path (they jump to a card) and the ? term-help buttons (component 66) are not the screen's actions. */
  primary: `${VISIBLE}
    const all = Array.from(document.querySelectorAll('[data-primary], button[data-kind=primary], a[data-button][data-kind=primary]')).filter(visible);
    const names = all.map(label);
    const hasActions = Array.from(document.querySelectorAll('main button, main a[data-button]')).some((e) => visible(e) && !e.closest('[role=tablist], .ds-segmented, .ds-chips, .ds-jobpath, table, [role=table], [role=row]') && !e.hasAttribute('data-term-help'));
    if (names.length > 1) return ['more than one primary: ' + names.join(' | ')];
    if (names.length === 0 && hasActions) return ['no primary button although the screen has actions'];
    return [];`,
  /** UX-17: exactly one visible h1. */
  h1: `${VISIBLE}
    const h = Array.from(document.querySelectorAll('h1')).filter(visible);
    return h.length === 1 ? [] : ['found ' + h.length + ' h1: ' + h.map(label).join(' | ')];`,
  /** UX-12: where rows can be ticked, Select all N and Clear are in the toolbar. */
  selectAll: `${VISIBLE}
    const ticks = Array.from(document.querySelectorAll('main tbody input[type=checkbox], main [role=row] input[type=checkbox], main li input[type=checkbox]')).filter((e) => e.getClientRects().length > 0);
    if (ticks.length === 0) return [];
    const buttons = Array.from(document.querySelectorAll('main button')).filter(visible).map((b) => (b.textContent || '').trim());
    const out = [];
    if (!buttons.some((t) => /^Select all \\d+/.test(t))) out.push('rows can be ticked but there is no "Select all N" button');
    if (!buttons.some((t) => t === 'Clear')) out.push('rows can be ticked but there is no "Clear" button');
    return out;`,
  /** UX-15, UX-49: nothing in a nav, a menu or a tab row scrolls sideways. */
  navScroll: `${VISIBLE}
    const bad = [];
    const roots = document.querySelectorAll('nav, [role=menu], [role=menubar], [role=tablist], .ds-topmenu, .ds-tabs, .ds-popover');
    const seen = new Set();
    for (const root of roots) {
      for (const e of [root, ...root.querySelectorAll('*')]) {
        if (seen.has(e) || !visible(e)) continue;
        seen.add(e);
        if (e.scrollWidth > e.clientWidth + 1 && e.clientWidth > 0) bad.push(label(e) + ' scrolls sideways (' + e.scrollWidth + ' px in ' + e.clientWidth + ' px)');
      }
    }
    return bad;`,
  /** UX-49 (LOOK29): nothing shows a horizontal scroll bar: the page does not overflow, and no box that scrolls (overflow auto or scroll) has more width than it shows. */
  noScrollbar: `${VISIBLE}
    const bad = [];
    const de = document.documentElement;
    if (de.scrollWidth > de.clientWidth) bad.push('the page scrolls sideways (' + de.scrollWidth + ' px in ' + de.clientWidth + ' px)');
    for (const e of document.querySelectorAll('body *')) {
      if (e.matches('input, textarea, select, option, svg, svg *') || !visible(e)) continue;
      const s = getComputedStyle(e);
      if ((s.overflowX === 'auto' || s.overflowX === 'scroll') && e.scrollWidth > e.clientWidth + 1) bad.push(label(e) + ' has a horizontal scroll bar (' + e.scrollWidth + ' px in ' + e.clientWidth + ' px)');
    }
    return bad;`,
  /** UX-69 (A1): header, menu, main, banner and key-hint frames share one left and right edge, within 1 px. */
  frameEdges: `${VISIBLE}
    const bands = Array.from(document.querySelectorAll('[data-frame-band]')).filter(visible);
    if (bands.length === 0) return document.querySelector('header.ds-header') ? ['no frame band found'] : []; // a signed-out page has no frame
    const edge = (e) => { const r = e.getBoundingClientRect(); const s = getComputedStyle(e); return [r.left + parseFloat(s.paddingLeft), r.right - parseFloat(s.paddingRight)]; };
    const [l0, r0] = edge(bands[0]);
    const bad = [];
    for (const b of bands.slice(1)) {
      const [l, r] = edge(b);
      if (Math.abs(l - l0) > 1 || Math.abs(r - r0) > 1) bad.push(label(b) + ' edges ' + Math.round(l) + '-' + Math.round(r) + ' differ from ' + Math.round(l0) + '-' + Math.round(r0));
    }
    return bad;`,
  /** UX-69 (LOOK9): on a signed-in page the title, the first menu item and the page content share the frame's left edge, and the content fills the frame to its right edge, within 1 px. The sign-in frame is narrow and has no header. */
  pageEdges: `${VISIBLE}
    const title = document.querySelector('.ds-header__product');
    const first = document.querySelector('.ds-topmenu__link');
    const main = document.querySelector('main');
    if (!title || !main) return [];
    const content = Array.from(main.children).filter(visible);
    if (content.length === 0) return [];
    const textLeft = (e) => { const r = document.createRange(); r.selectNodeContents(e); const b = r.getBoundingClientRect(); return b.width > 0 ? b.left : e.getBoundingClientRect().left; };
    const mp = getComputedStyle(main);
    const mr = main.getBoundingClientRect();
    const frameL = mr.left + parseFloat(mp.paddingLeft);
    const frameR = mr.right - parseFloat(mp.paddingRight);
    const bad = [];
    const tl = textLeft(title);
    if (Math.abs(tl - frameL) > 1) bad.push('title starts at ' + Math.round(tl) + ' but the frame at ' + Math.round(frameL));
    if (first) { const fl = first.getBoundingClientRect().left; if (Math.abs(fl - tl) > 1) bad.push('first menu item starts at ' + fl.toFixed(1) + ' but the title at ' + tl.toFixed(1)); }
    for (const c of content) {
      const r = c.getBoundingClientRect();
      if (Math.abs(r.left - frameL) > 1) bad.push(label(c) + ' starts at ' + Math.round(r.left) + ' but the frame at ' + Math.round(frameL));
      if (Math.abs(r.right - frameR) > 1) bad.push(label(c) + ' ends at ' + Math.round(r.right) + ' but the frame at ' + Math.round(frameR));
    }
    return bad;`,
  /** UX-69 (LOOK36): the phone's Menu button, the first label of every tab row and the page title share the frame's left edge, and no tab label is cut or wrapped inside itself. A label starts where the page starts, not one tab padding further in. */
  tabEdges: `${VISIBLE}
    const main = document.querySelector('main');
    if (!main) return [];
    const mp = getComputedStyle(main);
    const frameL = main.getBoundingClientRect().left + parseFloat(mp.paddingLeft);
    const textLeft = (e) => { const r = document.createRange(); r.selectNodeContents(e); const b = r.getBoundingClientRect(); return b.width > 0 ? b.left : e.getBoundingClientRect().left; };
    const bad = [];
    const toggle = Array.from(document.querySelectorAll('header ~ * button, header ~ button')).filter(visible).find((b) => /^(Menu:|Close the menu)/.test((b.textContent || '').trim()));
    if (toggle) {
      const l = toggle.getBoundingClientRect().left;
      if (Math.abs(l - frameL) > 1) bad.push('the Menu button box starts at ' + l.toFixed(1) + ' but the frame at ' + frameL.toFixed(1));
    }
    for (const list of document.querySelectorAll('[role=tablist]')) {
      if (!visible(list)) continue;
      const lr = list.getBoundingClientRect();
      const tabs = Array.from(list.querySelectorAll('[role=tab]')).filter(visible);
      const rows = new Map();
      for (const t of tabs) { const top = Math.round(t.getBoundingClientRect().top); if (!rows.has(top)) rows.set(top, t); }
      for (const t of rows.values()) {
        const l = textLeft(t);
        if (Math.abs(l - frameL) > 1) bad.push('tab "' + (t.textContent || '').trim() + '" starts a row at ' + l.toFixed(1) + ' but the frame at ' + frameL.toFixed(1));
      }
      for (const t of tabs) {
        const r = t.getBoundingClientRect();
        const name = '"' + (t.textContent || '').trim() + '"';
        if (r.right > lr.right + 1 || r.left < lr.left - 1) bad.push('tab ' + name + ' is cut by the tab row (' + Math.round(r.left) + '-' + Math.round(r.right) + ' in ' + Math.round(lr.left) + '-' + Math.round(lr.right) + ')');
        const tops = new Set();
        const rg = document.createRange(); rg.selectNodeContents(t);
        for (const x of rg.getClientRects()) tops.add(Math.round(x.top));
        if (tops.size > 1) bad.push('tab ' + name + ' wraps onto ' + tops.size + ' lines');
        if (t.scrollWidth > t.clientWidth + 1) bad.push('tab ' + name + ' is wider than its box');
      }
      const h1 = document.querySelector('main h1');
      if (h1 && visible(h1)) {
        const hl = textLeft(h1);
        if (tabs[0] && Math.abs(hl - textLeft(tabs[0])) > 1) bad.push('the title starts at ' + hl.toFixed(1) + ' but the first tab at ' + textLeft(tabs[0]).toFixed(1));
      }
    }
    return bad;`,
  /** UX-69 (LOOK36, tokens spacing): the block under a tab row (the page title, a pane, a table) is one page gap (--ds-space-5) below it. The gap rule of dom-layout judges the children of one group, and a tab row that sits in its own page has no sibling there, so this measures what is under it. */
  tabGap: `${VISIBLE}
    const probe = document.createElement('i');
    probe.style.cssText = 'position:absolute;visibility:hidden;width:0;height:0;padding-left:var(--ds-space-5)';
    document.body.appendChild(probe);
    const want = parseFloat(getComputedStyle(probe).paddingLeft) || 0;
    probe.remove();
    const bad = [];
    for (const list of document.querySelectorAll('[role=tablist]')) {
      if (!visible(list)) continue;
      const lr = list.getBoundingClientRect();
      let next = null;
      for (const e of document.querySelectorAll('main h1, main h2, main .ds-pane, main .ds-page-header, main .ds-breadcrumbs, main table, main form, main p, main .ds-state')) {
        if (!visible(e) || list.contains(e) || e.contains(list) || e.closest('.ds-action-bar') || (e.matches('h2') && e.closest('.ds-pane'))) continue; // a pane's title sits on the pane's border: the pane itself is measured; the sticky action bar floats over the page, it is not the block under the tabs
        const r = e.getBoundingClientRect();
        if (r.top < lr.bottom - 1 || r.top > lr.bottom + 400) continue;
        if (!next || r.top < next.getBoundingClientRect().top) next = e;
      }
      if (!next) continue;
      const gap = next.getBoundingClientRect().top - lr.bottom;
      if (gap < want - 1) bad.push(label(next) + ' is ' + Math.round(gap) + ' px under the tab row, the page gap is ' + want + ' px');
    }
    return bad;`,
  /** UX-69 (A3): the open menu item has no box: no side border, no outline, no fill. */
  navBox: `${VISIBLE}
    const bad = [];
    for (const e of document.querySelectorAll('.ds-topmenu [aria-current=page]')) {
      const s = getComputedStyle(e);
      for (const side of ['Left', 'Right', 'Top']) if (parseFloat(s['border' + side + 'Width']) > 0 && s['border' + side + 'Style'] !== 'none') bad.push(label(e) + ' has a ' + side.toLowerCase() + ' border');
      if (s.backgroundColor !== 'rgba(0, 0, 0, 0)') bad.push(label(e) + ' has a fill ' + s.backgroundColor);
      if (parseFloat(s.borderBottomWidth) < 3) bad.push(label(e) + ' underline is under 3 px');
    }
    return bad;`,
  /** UX-69 (A6): no table header cell wraps. Phone width hides the header row, so it is judged where it shows. */
  tableHead: `${VISIBLE}
    const bad = [];
    for (const th of document.querySelectorAll('main th')) {
      if (!visible(th)) continue;
      const tops = [];
      const walk = document.createTreeWalker(th, NodeFilter.SHOW_TEXT);
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        if (!(n.textContent || '').trim()) continue;
        const r = document.createRange(); r.selectNodeContents(n);
        for (const x of r.getClientRects()) tops.push(Math.round(x.top));
      }
      const lines = new Set(tops);
      if (lines.size > 1) bad.push(label(th) + ' wraps onto ' + lines.size + ' lines');
    }
    return bad;`,
  /** UX-69 (A7): no button sits on a pane border. */
  paneButtons: `${VISIBLE}
    const bad = [];
    for (const pane of document.querySelectorAll('.ds-pane')) {
      const p = pane.getBoundingClientRect();
      for (const b of pane.querySelectorAll('button, a[data-button]')) {
        if (!visible(b)) continue;
        const head = b.closest('thead');
        if (head && head.getBoundingClientRect().width <= 2) continue; // a table head folded away at phone width
        const r = b.getBoundingClientRect();
        const cross = (edge, lo, hi) => lo < edge - 0.5 && hi > edge + 0.5;
        if (cross(p.top, r.top, r.bottom) || cross(p.bottom, r.top, r.bottom)) bad.push(label(b) + ' overlaps the border of pane ' + (pane.getAttribute('aria-label') || ''));
      }
    }
    return bad;`,
  /** UX-69 (A8): each footer hint key appears once. */
  hintKeys: `${VISIBLE}
    const keys = Array.from(document.querySelectorAll('.ds-keyhints kbd')).map((k) => (k.textContent || '').toLowerCase().replace(/[\\s\\/,]+/g, ''));
    return keys.filter((k, i) => keys.indexOf(k) !== i).map((k) => 'key hint ' + k + ' is listed twice');`,
  /** OPS48 (UX-10, UX-51): the title of a pane never covers the title of the pane it sits in (a Card in a Section). */
  titleOverlap: `${VISIBLE}
    const bad = [];
    for (const t of document.querySelectorAll('.ds-pane__title')) {
      if (!visible(t)) continue;
      const a = t.getBoundingClientRect();
      let outer = t.closest('.ds-pane')?.parentElement?.closest('.ds-pane');
      while (outer) {
        const o = outer.querySelector(':scope > .ds-pane__title');
        if (o && visible(o)) {
          const b = o.getBoundingClientRect();
          if (a.left < b.right - 0.5 && a.right > b.left + 0.5 && a.top < b.bottom - 0.5 && a.bottom > b.top + 0.5)
            bad.push('title "' + (t.textContent || '').trim().slice(0, 30) + '" covers the title "' + (o.textContent || '').trim().slice(0, 30) + '" of its section');
        }
        outer = outer.parentElement?.closest('.ds-pane');
      }
    }
    return bad;`,
  /** Look Option B: every page section, list and table of the main area sits inside a Pane. */
  pane: `${VISIBLE}
    const main = document.querySelector('main');
    if (!main) return ['no main'];
    const inPane = (e) => !!e.closest('.ds-pane');
    const ignore = (e) => !!e.closest('nav, header, footer, .ds-pane, .ds-list-toolbar, .ds-segmented, .ds-chips, .ds-pager, [role=tablist], [role=listbox], [role=dialog], .ds-wizard, .ds-grant, .ds-strip, .ds-page-header, .ds-breadcrumbs, details, .ds-state');
    const bad = [];
    for (const e of main.querySelectorAll('section, table, ul, ol, [role=table], [role=list], h2')) {
      if (!visible(e) || inPane(e)) continue;
      if (e.matches('ul, ol') && ignore(e)) continue;
      if (e.matches('section, table, [role=table], h2') && ignore(e)) continue;
      if (e.matches('ul, ol') && e.children.length === 0) continue;
      bad.push(label(e) + ' is outside a pane');
    }
    return bad;`,
};
export type Measure = keyof typeof MEASURES;

export async function measure(page: Page, what: Measure): Promise<string[]> {
  return page.evaluate<string[]>(`(() => { ${MEASURES[what]} })()`);
}

/** Opens the route at each width and mode and returns the problem lines of one measure, prefixed with where. */
export async function measureEverywhere(
  page: Page,
  route: Pick<ConsoleRoute, 'pattern' | 'url' | 'signedOut'>,
  what: Measure,
  state: ScreenState = 'data',
  widths: readonly { width: number; height: number }[] = WIDTHS,
): Promise<string[]> {
  const out: string[] = [];
  for (const mode of MODES) {
    await openRoute(page, route, { mode, state });
    for (const w of widths) {
      await page.setViewportSize(w);
      await frames(page);
      for (const line of await measure(page, what))
        out.push(`${route.pattern} ${state} ${mode} ${w.width}px: ${line}`);
    }
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  }
  return out;
}

/**
 * UX-42, UX-71, component 5 and 48 (RES21 B01, B02, B03, B19, N3, N5): on a page that scrolls and has an action
 * bar, the bar is inside the window at scroll 0, half way and at the end, and at the end the last text ends above
 * the bar and above the key-hint bar. Returns problem lines; a page that does not scroll has none.
 */
export async function stickyWalk(page: Page): Promise<string[]> {
  const out: string[] = [];
  const info = await page.evaluate(() => ({
    max: document.documentElement.scrollHeight - window.innerHeight,
    has: document.querySelector('.ds-action-bar') !== null,
  }));
  if (!info.has || info.max < 40) return out;
  for (const frac of [0, 0.5, 0.97, 1]) {
    const bars = await page.evaluate((f) => {
      window.scrollTo(
        0,
        Math.round((document.documentElement.scrollHeight - window.innerHeight) * f),
      );
      return Array.from(document.querySelectorAll('.ds-action-bar')).map((bar) => {
        const r = bar.getBoundingClientRect();
        return {
          name:
            (bar.getAttribute('aria-label') ?? 'action bar') +
            ':' +
            (bar.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 30),
          top: r.top,
          bottom: r.bottom,
          height: r.height,
          position: getComputedStyle(bar).position,
          inset: getComputedStyle(bar).insetBlockEnd,
          window: window.innerHeight,
          page: document.documentElement.scrollHeight,
          sticky: bar.getAttribute('data-sticky'),
          hints: Array.from(document.querySelectorAll('.ds-keyhints'))
            .map((h) => h.getBoundingClientRect())
            .filter((h) => h.height > 0 && h.top < window.innerHeight)
            .map((h) => h.top),
        };
      });
    }, frac);
    for (const b of bars)
      for (const hintTop of b.hints)
        if (b.height > 0 && b.bottom > hintTop + 1)
          out.push(
            `${b.name} overlaps the key-hint bar by ${Math.round(b.bottom - hintTop)} px at ${Math.round(frac * 100)}% of the page`,
          );
    for (const b of bars)
      if (b.height > 0 && b.sticky !== 'false' && (b.top < -1 || b.bottom > b.window + 1))
        out.push(
          `${b.name} (position ${b.position}, bottom offset ${b.inset}) is at ${Math.round(b.top)}..${Math.round(b.bottom)} in a ${b.window} px window at ${Math.round(frac * 100)}% of a ${b.page} px page`,
        );
  }
  const covered = await page.evaluate(() => {
    window.scrollTo(0, document.documentElement.scrollHeight);
    const bars = Array.from(document.querySelectorAll('.ds-action-bar, .ds-keyhints'))
      .map((b) => b.getBoundingClientRect())
      .filter((r) => r.height > 0 && r.top < window.innerHeight);
    const walk = document.createTreeWalker(
      document.querySelector('main') ?? document.body,
      NodeFilter.SHOW_TEXT,
    );
    const hit: string[] = [];
    for (let n = walk.nextNode(); n; n = walk.nextNode()) {
      const el = n.parentElement;
      if (!el || !(n.textContent ?? '').trim()) continue;
      if (el.closest('.ds-action-bar, .ds-keyhints, .ds-visually-hidden, .ds-short, svg'))
        continue;
      if (!el.checkVisibility({ contentVisibilityAuto: true, visibilityProperty: true }))
        continue;
      const head = el.closest('thead');
      if (head && head.getBoundingClientRect().width <= 2) continue; // folded away at phone width
      const rg = document.createRange();
      rg.selectNodeContents(n);
      for (const raw of Array.from(rg.getClientRects())) {
        let { left, right, top, bottom } = raw;
        for (let x: Element | null = el; x && x !== document.body; x = x.parentElement) {
          const st = getComputedStyle(x);
          if (st.overflowX === 'visible' && st.overflowY === 'visible') continue;
          const b = x.getBoundingClientRect();
          if (st.overflowX !== 'visible') {
            left = Math.max(left, b.left);
            right = Math.min(right, b.right);
          }
          if (st.overflowY !== 'visible') {
            top = Math.max(top, b.top);
            bottom = Math.min(bottom, b.bottom);
          }
        }
        if (right - left <= 2 || bottom - top <= 2) continue;
        for (const b of bars)
          if (
            bottom > b.top + 3 &&
            top < b.bottom - 3 &&
            right > b.left &&
            left < b.right
          )
            hit.push((n.textContent ?? '').trim().slice(0, 30));
      }
    }
    return hit.slice(0, 3);
  });
  if (covered.length)
    out.push(`at the end of the page the bar covers "${covered.join('", "')}"`);
  await page.evaluate(() => window.scrollTo(0, 0));
  return out;
}

/**
 * RES21 B02: the Save bar of an edit screen only exists after a change. Changes the first editable control of the
 * page (a text field, else a switch or box) and walks the bar. Returns no lines when the page has nothing to edit.
 */
export async function stickyWalkAfterChange(page: Page): Promise<string[]> {
  const text = page
    .locator(
      'main input:not([type]), main input[type=text], main input[type=number], main textarea',
    )
    .and(page.locator(':enabled:visible'))
    .first();
  const box = page.locator('main input[type=checkbox]:enabled:visible').first();
  if (await text.count()) {
    if ((await text.getAttribute('type')) === 'number') await text.fill('7');
    else await text.fill(`${await text.inputValue()}x`);
  } else if (await box.count()) await box.click({ force: true });
  else return [];
  await frames(page);
  return stickyWalk(page);
}

/** `stickyWalk` on the route at each width and mode, prefixed with where. */
export async function stickyEverywhere(
  page: Page,
  route: Pick<ConsoleRoute, 'pattern' | 'url' | 'signedOut'>,
  state: ScreenState = 'data',
  widths: readonly { width: number; height: number }[] = TEXT_WIDTHS,
  walk: (page: Page) => Promise<string[]> = stickyWalk,
): Promise<string[]> {
  const out: string[] = [];
  for (const mode of MODES) {
    await openRoute(page, route, { mode, state });
    for (const w of widths) {
      await page.setViewportSize(w);
      await frames(page);
      for (const line of await walk(page))
        out.push(`${route.pattern} ${state} ${mode} ${w.width}px: ${line}`);
    }
    await page.unrouteAll({ behavior: 'ignoreErrors' });
  }
  return out;
}
