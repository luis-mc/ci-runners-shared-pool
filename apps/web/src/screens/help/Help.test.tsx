// LOOK44, LOOK47, LOOK63: the Help page (13 sections, nine question cards), the job path and the ? popover.
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { job } from '../../testing/jobsData';
import { machine, overview, renderApp, runner, signedIn } from '../../testing/mock';
import { CARDS, GUIDE, TOPICS } from './helpContent';

afterEach(cleanup);

const MENU = [
  'Start here',
  'Where things are',
  'Pool and load budget',
  'Runner types',
  'Size classes',
  'Admission and passing',
  'Reservation',
  'Job size',
  'Before assignment',
  'Gating',
  'Routing labels',
  'Timers',
  'Reading Queue Analysis',
];

describe('Help (LOOK63)', () => {
  it('has the 13 sections of the approved prototype in its order', async () => {
    renderApp(signedIn(), '/help');
    const menu = await screen.findByRole('navigation', { name: 'Help sections' });
    const links = within(menu).getAllByRole('link');
    expect(links.map((a) => a.textContent?.replace(/^\d+/, ''))).toEqual(MENU);
    expect(TOPICS.map((t) => t.label)).toEqual(MENU);
    expect(
      within(menu)
        .getByRole('link', { name: /Start here/ })
        .getAttribute('aria-current'),
    ).toBe('page');
  });

  it('opens at /help with the picture and nine cards, each with an Open link, and no wizard', async () => {
    renderApp(signedIn(), '/help');
    expect(await screen.findByRole('heading', { level: 1, name: 'Help' })).toBeTruthy();
    expect(
      screen.getByText('How crsp runs jobs. Pictures use your own pool.'),
    ).toBeTruthy();
    expect(screen.getByRole('region', { name: 'How a job gets run' })).toBeTruthy();
    const questions = screen.getByRole('region', { name: 'Questions' });
    expect(CARDS).toHaveLength(9);
    expect(within(questions).getAllByRole('heading', { level: 3 })).toHaveLength(9);
    for (const c of CARDS) {
      const card = within(questions).getByRole('region', { name: c.title });
      expect(within(card).getByText(c.answer)).toBeTruthy();
      expect(
        within(card).getByRole('link', { name: c.link.label }).getAttribute('href'),
      ).toBe(c.link.to);
      expect(c.link.label.startsWith('Open')).toBe(true);
    }
    expect(screen.queryByRole('list', { name: 'Steps' })).toBeNull();
    for (const n of ['Back', 'Next', 'Start again']) {
      expect(screen.queryByRole('button', { name: n })).toBeNull();
    }
  });

  it('draws the picture read only: no box is a button', async () => {
    renderApp(signedIn(), '/help');
    const picture = await screen.findByRole('list', { name: 'How a job gets run' });
    expect(within(picture).queryAllByRole('button')).toHaveLength(0);
    expect(within(picture).getAllByRole('listitem').length).toBeGreaterThanOrEqual(7);
  });

  it('keeps an address that ends in a card, with every card still shown', async () => {
    renderApp(signedIn(), '/help#q-elsewhere');
    expect(
      await screen.findByRole('heading', { name: 'What does Run elsewhere mean?' }),
    ).toBeTruthy();
    expect(
      screen.getByRole('heading', { name: 'How do I make it faster?' }),
    ).toBeTruthy();
  });

  it('shows each new section at its address with its title, lead and list', async () => {
    const slugs = Object.keys(GUIDE);
    expect(slugs).toHaveLength(10);
    for (const slug of slugs) {
      const g = GUIDE[slug]!;
      expect(TOPICS.some((t) => t.to === `/help/${slug}`)).toBe(true);
      renderApp(signedIn(), `/help/${slug}`);
      expect(await screen.findByRole('region', { name: g.title })).toBeTruthy();
      expect(screen.getByText(g.lead)).toBeTruthy();
      for (const p of g.points) expect(screen.getByText(p)).toBeTruthy();
      expect(screen.queryByRole('region', { name: 'Questions' })).toBeNull();
      cleanup();
    }
  });

  it('writes Admission and passing as the prototype does', async () => {
    renderApp(signedIn(), '/help/admission');
    const pane = await screen.findByRole('region', { name: 'Admission and passing' });
    expect(
      within(pane).getByText(
        'A job is admitted when its CPU and memory fit the free pool and the class ceiling. Lighter jobs may pass a big one that does not fit yet.',
      ),
    ).toBeTruthy();
    expect(
      within(pane).getByText(
        'Check 1: class ceiling. Check 2: free CPU. Check 3: free memory.',
      ),
    ).toBeTruthy();
    expect(
      within(pane).getByText('A big job can be passed up to 3 times, or for 10 min.'),
    ).toBeTruthy();
    expect(within(pane).getByText('Then it is reserved (see Reservation).')).toBeTruthy();
    expect(
      within(pane)
        .getByRole('link', { name: 'See it live in Pool > Live' })
        .getAttribute('href'),
    ).toBe('/pool/live');
    expect(
      await screen.findByRole('region', { name: 'In your pool right now' }),
    ).toBeTruthy();
  });

  it('shows the first waiting job and why it waits on Admission and passing', async () => {
    const api = signedIn().on(
      'GET',
      '/api/v1/overview',
      overview({
        queued_count: 1,
        queued: [
          job({
            job_name: 'e2e-1',
            repository: 'octo/app',
            state: 'queued',
            wait_reason: 'free_slot',
            wait_text:
              'the job needs 4 CPUs and 8 GiB; the pool has 2 CPUs and 4 GiB free',
            queue_position: 1,
          }),
        ],
      }),
    );
    renderApp(api, '/help/admission');
    const now = await screen.findByRole('region', { name: 'In your pool right now' });
    expect(await within(now).findByText('e2e-1 octo/app')).toBeTruthy();
    expect(
      within(now).getByText(
        'the job needs 4 CPUs and 8 GiB; the pool has 2 CPUs and 4 GiB free',
      ),
    ).toBeTruthy();
  });

  it('keeps Where things are and Reading Queue Analysis', async () => {
    renderApp(signedIn(), '/help/where');
    expect(
      await screen.findByRole('table', { name: 'The parts of the console' }),
    ).toBeTruthy();
    cleanup();
    renderApp(signedIn(), '/help/queue');
    expect(
      await screen.findByRole('region', { name: 'In your pool right now' }),
    ).toBeTruthy();
  });
});

describe('Help is the picture and the cards (LOOK48, LOOK63)', () => {
  it('shows a live example in each box from real data, else the short meaning', async () => {
    const api = signedIn()
      .on(
        'GET',
        '/api/v1/overview',
        overview({
          queued_count: 2,
          running: [
            job({
              repository: 'octo/app',
              run_id: 8133,
              trigger: 'push',
              job_name: 'e2e',
              labels: ['self-hosted', 'linux'],
              state: 'running',
            }),
          ],
        }),
      )
      .on('GET', '/api/v1/runners', {
        items: [runner({ name: 'crsp-linux-1' })],
      });
    renderApp(api, '/help');
    const path = await screen.findByRole('list', { name: 'How a job gets run' });
    expect(await within(path).findByText('octo/app')).toBeTruthy();
    for (const t of [
      '#8133, push',
      'e2e + labels',
      '2 waiting',
      '1 of 4 busy',
      'crsp-linux-1',
      'build-1',
    ]) {
      expect(within(path).getByText(t)).toBeTruthy();
    }
    expect(within(path).queryByText('a GitHub project')).toBeNull();
  });

  it('shows the short meaning when nothing is known yet', async () => {
    renderApp(
      signedIn()
        .on('GET', '/api/v1/machines', { items: [] })
        .on('GET', '/api/v1/runners', { items: [] }),
      '/help',
    );
    const path = await screen.findByRole('list', { name: 'How a job gets run' });
    expect(await within(path).findByText('a GitHub project')).toBeTruthy();
    expect(within(path).getByText('your computer')).toBeTruthy();
    expect(within(path).getByText('room for one job')).toBeTruthy();
  });
});

describe('the ? beside a term (LOOK44, LOOK63)', () => {
  it('opens a short answer with a link to its Help section and closes with Escape', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/machines/:id', machine()),
      '/setup/machines/m1',
    );
    const ask = await screen.findByRole('button', { name: 'What does Slots mean?' });
    expect(ask.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(ask);
    const panel = await screen.findByRole('group', { name: 'Slots' });
    expect(within(panel).getByText(/Room for one job at a time/)).toBeTruthy();
    expect(
      within(panel)
        .getByRole('link', { name: 'More about this in Help' })
        .getAttribute('href'),
    ).toBe('/help/pool-and-load-budget');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('group', { name: 'Slots' })).toBeNull();
  });
});
