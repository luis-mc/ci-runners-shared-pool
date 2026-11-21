// LOOK44, LOOK47: the Help page (layout G, no wizard), the job path and the ? popover.
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { job } from '../../testing/jobsData';
import { machine, overview, renderApp, runner, signedIn } from '../../testing/mock';
import { CARDS, PART_CARD, PATH_PARTS } from './helpContent';

afterEach(cleanup);

const withBudget = () =>
  signedIn().on('GET', '/api/v1/machines', {
    items: [
      machine({
        name: 'build-1',
        slots_used: 2,
        settings: {
          ...machine().settings,
          job_slots: 4,
          budget_memory_pct: 70,
          budget_cpu_pct: 55,
        },
      }),
    ],
  });

describe('Help (LOOK47)', () => {
  it('opens at /help with the picture on top and every card visible, no wizard', async () => {
    renderApp(signedIn(), '/help');
    expect(await screen.findByRole('heading', { level: 1, name: 'Help' })).toBeTruthy();
    await screen.findByRole('region', {
      name: 'Questions, in the order a job meets them',
    });
    const nav = screen.getByRole('navigation', { name: 'Areas' });
    expect(
      within(nav).getByRole('link', { name: 'Help' }).getAttribute('aria-current'),
    ).toBe('page');
    expect(screen.getByRole('region', { name: 'How a job gets run' })).toBeTruthy();
    expect(
      screen.getByRole('region', { name: 'Questions, in the order a job meets them' }),
    ).toBeTruthy();
    for (const t of [
      'What does shared mean?',
      'What is a slot?',
      'What is the Doctor?',
      'What is the tray app?',
      'What does Run elsewhere mean?',
    ]) {
      expect(screen.getByRole('heading', { name: t })).toBeTruthy();
    }
    expect(screen.getAllByRole('heading', { level: 3 }).length).toBe(CARDS.length);
    expect(screen.queryByRole('list', { name: 'Steps' })).toBeNull();
    for (const n of ['Back', 'Next', 'Start again']) {
      expect(screen.queryByRole('button', { name: n })).toBeNull();
    }
  });

  it('presses a part of the picture and nothing is locked', async () => {
    renderApp(signedIn(), '/help');
    await screen.findByRole('region', {
      name: 'Questions, in the order a job meets them',
    });
    const box = await screen.findByRole<HTMLButtonElement>('button', {
      name: /^Slot\s*1 of 4 busy/,
    });
    expect(box.disabled).toBe(false);
    fireEvent.click(box);
    expect(screen.getByRole('heading', { name: 'What is a slot?' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'What does shared mean?' })).toBeTruthy();
  });

  it('keeps an address that ends in a card, with every card still shown', async () => {
    renderApp(signedIn(), '/help#q-budget');
    expect(
      await screen.findByRole('heading', { name: 'What is the load budget?' }),
    ).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'What is the Doctor?' })).toBeTruthy();
  });

  it("draws the machine's real load budget in a picture, never a fixed 85% and 60%", async () => {
    renderApp(withBudget(), '/help#q-budget');
    expect(await screen.findByText('Memory 40% of 70%')).toBeTruthy();
    expect(screen.getByText('CPU 20% of 55%')).toBeTruthy();
    expect(screen.queryByText(/85/)).toBeNull();
  });

  it('draws the slots of the machine on the slot card', async () => {
    renderApp(withBudget(), '/help#q-slot');
    expect(await screen.findByText('2 of 4 slots busy')).toBeTruthy();
    expect(screen.getByText(/Pool size makes slots/)).toBeTruthy();
  });

  it('lists every machine with its numbers when there are two', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/machines', {
        items: [
          machine({ name: 'build-1', slots_used: 2 }),
          machine({ id: 'm2', name: 'win-box', slots_used: 0 }),
        ],
      }),
      '/help#q-slot',
    );
    expect(await screen.findByText('2 of 4 slots in use')).toBeTruthy();
    expect(screen.getByText('0 of 4 slots in use')).toBeTruthy();
  });

  it('says so when there is no machine yet', async () => {
    renderApp(signedIn().on('GET', '/api/v1/machines', { items: [] }), '/help#q-slot');
    expect((await screen.findAllByText(/No machine yet\./)).length).toBeGreaterThan(0);
  });
});

describe('Help is layout G (LOOK48)', () => {
  it('has the intro line, the note under the picture and the questions title', async () => {
    renderApp(signedIn(), '/help');
    expect(
      await screen.findByText(
        'One picture, then the questions people ask. Each card names its box.',
      ),
    ).toBeTruthy();
    expect(
      screen.getByText('1 to 3 happen on GitHub. 4 to 7 happen in your pool.'),
    ).toBeTruthy();
    expect(
      screen.getByRole('region', { name: 'Questions, in the order a job meets them' }),
    ).toBeTruthy();
  });

  it('orders the cards by the box they explain and every card names its box', async () => {
    renderApp(signedIn(), '/help');
    await screen.findByRole('region', {
      name: 'Questions, in the order a job meets them',
    });
    const order = PATH_PARTS.map((p) => p.id);
    const boxes = CARDS.map((c) => order.indexOf(c.box));
    expect(boxes).toEqual([...boxes].sort((a, b) => a - b));
    const badges = screen
      .getAllByRole('link', { name: /^Box \d: / })
      .map((a) => a.textContent);
    expect(badges).toHaveLength(CARDS.length);
    expect(badges[0]).toBe('1Box 1: Repository');
    expect(badges).toContain('5Box 5: Slot');
    expect(badges).toContain('7Box 7: Machine');
    expect(PART_CARD.slot).toBe('q-slot');
    expect(PART_CARD.queue).toBe('q-waiting');
  });

  it('lights the diagram box when its badge is pressed', async () => {
    renderApp(signedIn(), '/help');
    await screen.findByRole('region', {
      name: 'Questions, in the order a job meets them',
    });
    const slotBox = screen.getByRole('button', { name: /^Slot/ });
    expect(slotBox.getAttribute('aria-pressed')).toBeNull();
    const card = screen.getByRole('region', { name: 'What is a slot?' });
    fireEvent.click(within(card).getByRole('link', { name: /Box 5: Slot/ }));
    expect(slotBox.getAttribute('aria-pressed')).toBe('true');
    expect(
      screen.getByRole('button', { name: /^Run(?!ner)/ }).getAttribute('aria-pressed'),
    ).toBe('false');
  });

  it('puts a small picture on the cards that have one', async () => {
    renderApp(signedIn(), '/help');
    await screen.findByRole('region', {
      name: 'Questions, in the order a job meets them',
    });
    for (const t of [
      'What does shared mean?',
      'Why is my job waiting?',
      'What is a slot?',
      'What is the load budget?',
      'What is a runner type?',
      'What does Run elsewhere mean?',
      'What is the tray app?',
      'What is the Doctor?',
    ]) {
      const card = screen.getByRole('region', { name: t });
      expect(within(card).getAllByRole('img').length).toBe(1);
    }
  });

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

describe('the ? beside a term (LOOK44)', () => {
  it('opens a short answer with a link to the Help card and closes with Escape', async () => {
    renderApp(
      withBudget().on('GET', '/api/v1/machines/:id', machine()),
      '/pool/machines/m1',
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
    ).toBe('/help#q-slot');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('group', { name: 'Slots' })).toBeNull();
  });
});
