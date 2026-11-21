// machines R5.68, OPS61: the Pool size block shows what the PC itself uses (measured p95 or "measuring, N h of 24"),
// the room that follows from it, a warning (not a block) for a Custom size that leaves the PC less than that, and on
// WSL2 the .wslconfig memory cap with the lines to copy.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { machine, renderApp, signedIn } from '../../testing/mock';
import { liveLine, measuredBasis, roomHow, wslAdvice, wslLines } from './PoolSize';

afterEach(cleanup);

const base = {
  known: true,
  ok: true,
  needs_shrink_confirm: false,
  nearest: [],
  total_gib: 32,
  used_outside_gib: 8,
  memory_budget_gib: 27.2,
  room_gib: 19.2,
  preset_room_gib: 19.2,
  safety_margin_gib: 1,
  cpu_budget: 4,
  decided: 'fits',
  current_memory_gib: 0,
  current_cpus: 0,
  recommended_half: { memory_gib: 9, cpus: 2 },
  recommended_three_quarters: { memory_gib: 14, cpus: 3 },
};
const measured = {
  ...base,
  basis: 'measured' as const,
  host_p95_gib: 10.2,
  host_span_hours: 170,
  measured_room_gib: 16,
  preset_room_gib: 16,
  recommended_half: { memory_gib: 8, cpus: 2 },
  recommended_three_quarters: { memory_gib: 12, cpus: 3 },
  leaves_gib: 20,
  leaves_less_than_host: false,
};

describe('Pool size, the measured basis', () => {
  it('says what Windows and the apps use, or how far the measuring has come', () => {
    expect(measuredBasis(measured, 'windows')).toBe(
      'Windows and your apps use up to 10.2 GiB (p95, 7 days)',
    );
    expect(measuredBasis(measured, 'linux')).toBe(
      'This PC and its programs use up to 10.2 GiB (p95, 7 days)',
    );
    expect(
      measuredBasis({ ...base, basis: 'measuring', host_span_hours: 6.9 }, 'windows'),
    ).toBe('measuring, 6 h of 24');
    expect(
      measuredBasis({ ...base, basis: 'measuring', host_span_hours: 24.5 }, 'windows'),
    ).toBe('measuring, 23 h of 24');
    expect(measuredBasis(base, 'windows')).toBeUndefined();
  });

  it('explains the room from the measured figure, or the fixed share while measuring', () => {
    expect(roomHow(measured)).toBe(
      'Room for the pool: 32 GiB of memory, times the budget, less what the PC uses (10.2 GiB) and 1 GiB to spare.',
    );
    expect(roomHow({ ...base, basis: 'measuring' })).toMatch(/^Until 24 h are measured/);
    expect(roomHow(base)).toBeUndefined();
  });

  it('warns, and does not block, when a size leaves the PC less than its own use', () => {
    const warn = { ...measured, leaves_less_than_host: true, leaves_gib: 8 };
    const line = liveLine(warn, 24, 4, 0, 0);
    expect(line.tone).toBe('needs-you');
    expect(line.text).toBe(
      'This size fits the budget, but leaves 8 GiB for this PC, less than the 10.2 GiB it uses (p95, 7 days).',
    );
    expect(liveLine(measured, 12, 4, 0, 0).tone).toBe('done');
    // A shrink keeps its own words and carries the warning as a note.
    const shrink = liveLine(warn, 12, 4, 24, 4);
    expect(shrink.text).toMatch(/^This lowers the pool size/);
    expect(shrink.notes[0]).toMatch(/leaves 8 GiB for this PC/);
  });

  it('gives the WSL cap block only for a pool in WSL2, with the lines that match the pool', () => {
    expect(wslAdvice(measured, 12)).toBeUndefined();
    const w = { ...measured, wsl_pool: true, wsl_cap_set: false };
    expect(wslAdvice(w, 12)).toEqual({
      capNow: 'not set (WSL takes half of the PC)',
      recommended: '12 GiB',
      lines: '[wsl2]\nmemory=12GB',
      warning: undefined,
    });
    expect(wslLines(10.5)).toBe('[wsl2]\nmemory=10.5GB');
    const over = wslAdvice(
      { ...w, wsl_cap_set: true, wsl_cap_gib: 28, wsl_cap_above_pc: true },
      12,
    );
    expect(over?.capNow).toBe('28 GiB');
    expect(over?.warning).toBe(
      'The WSL memory cap (28 GiB) is more than the PC can spare after what it uses (10.2 GiB).',
    );
    expect(wslAdvice(w, 0)).toBeUndefined();
  });
});

describe('Machine detail, the measured Pool size block', () => {
  const api = (check: Record<string, unknown>, m = machine()) =>
    signedIn()
      .on('GET', '/api/v1/machines/:id', m)
      .on('POST', '/api/v1/machines/:id/pool-size-check', check)
      .on('PATCH', '/api/v1/machines/:id', m);

  it('shows the measured basis and the room', async () => {
    renderApp(api(measured, machine({ os: 'windows' })), '/pool/machines/m1');
    expect(
      await screen.findByText('Windows and your apps use up to 10.2 GiB (p95, 7 days)'),
    ).toBeTruthy();
    expect(screen.getByText('16 GiB')).toBeTruthy();
    expect(screen.getByText(/Room for the pool: 32 GiB of memory/)).toBeTruthy();
    expect(
      await screen.findByRole('radio', {
        name: /half of the free room \(8 GiB, 2 CPUs\)/,
      }),
    ).toBeTruthy();
  });

  it('says it is measuring until 24 h, and keeps the fixed share', async () => {
    renderApp(
      api(
        { ...base, basis: 'measuring', host_span_hours: 5 },
        machine({ os: 'windows' }),
      ),
      '/pool/machines/m1',
    );
    expect(await screen.findByText('measuring, 5 h of 24')).toBeTruthy();
    expect(screen.getByText('19.2 GiB')).toBeTruthy();
    expect(
      await screen.findByRole('radio', {
        name: /half of the free room \(9 GiB, 2 CPUs\)/,
      }),
    ).toBeTruthy();
  });

  it('a Custom size under the PC use shows the warning and Save stays possible', async () => {
    const warn = {
      ...measured,
      leaves_less_than_host: true,
      leaves_gib: 8,
      room_gib: 25,
    };
    renderApp(api(warn), '/pool/machines/m1');
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLInputElement>('radio', { name: /Custom/ }).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole('radio', { name: /Custom/ }));
    fireEvent.click(await screen.findByRole('button', { name: /Pool memory/ }));
    const dialog = await screen.findByRole('dialog');
    for (const k of ['2', '4'])
      fireEvent.click(within(dialog).getByRole('button', { name: k }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }));
    expect(
      await screen.findByText(/leaves 8 GiB for this PC, less than the 10.2 GiB/),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLButtonElement>('button', { name: 'Save' }).disabled,
      ).toBe(false),
    );
  });

  it('shows the WSL cap and the lines to copy on a WSL2 pool, and never writes the file', async () => {
    const wsl = {
      ...measured,
      wsl_pool: true,
      wsl_cap_set: true,
      wsl_cap_gib: 28,
      wsl_cap_above_pc: true,
    };
    const a = api(
      wsl,
      machine({
        os: 'windows',
        settings: { ...machine().settings, pool_memory_gib: 12, pool_cpus: 4 },
      }),
    );
    renderApp(a, '/pool/machines/m1');
    expect(await screen.findByText('WSL memory cap now')).toBeTruthy();
    expect(screen.getByText('28 GiB')).toBeTruthy();
    expect(screen.getByText('Recommended WSL memory cap')).toBeTruthy();
    expect(screen.getByLabelText('Recommended .wslconfig lines').textContent).toBe(
      '[wsl2]\nmemory=12GB',
    );
    expect(screen.getByText(/WSL must restart for a change to apply/)).toBeTruthy();
    expect(screen.getByText(/more than the PC can spare/)).toBeTruthy();
    expect(a.calls.filter((c) => c.method === 'PATCH')).toHaveLength(0);
  });

  it('shows no WSL block off WSL2', async () => {
    renderApp(api(measured), '/pool/machines/m1');
    await screen.findByText(/use up to 10.2 GiB/);
    expect(screen.queryByText('WSL memory cap now')).toBeNull();
  });
});
