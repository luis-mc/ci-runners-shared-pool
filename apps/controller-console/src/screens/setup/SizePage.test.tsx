// LOOK64: Setup > Size and backends: the Size classes table, the Backends table, then Demand.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';
import { backendRows } from './SizePage';

afterEach(cleanup);

const backend = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  present: true,
  usable: true,
  allowed: true,
  ...over,
});

describe('backendRows', () => {
  it('counts the machines a backend is usable and allowed on, with its version', () => {
    const rows = backendRows([
      {
        backends: [
          backend('docker', { version: '27' }),
          backend('firecracker', { usable: false, present: false }),
        ],
      },
      { backends: [backend('docker', { allowed: false })] },
    ] as never);
    expect(rows).toEqual([
      { name: 'docker', allowed: 1, usable: 2, machines: 2, version: '27' },
    ]);
  });
});

describe('Size and backends', () => {
  it('shows the Size classes and the Backends tables above Demand', async () => {
    const api = signedIn().on('GET', '/api/v1/runner-classes', {
      preset: 'standard',
      presets: ['standard', 'compact', 'memory-heavy', 'custom'],
      classes: [
        { class: 'small', cpus: 2, memory_gib: 4 },
        { class: 'xl', cpus: 16, memory_gib: 32 },
      ],
      hints: [],
      unheld: [],
      reserve_after_passes: 3,
      reserve_after_minutes: 10,
    });
    renderApp(api, '/setup/size');
    const sizes = await screen.findByRole('table', { name: 'Size classes' });
    expect(
      within(sizes)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Class', 'CPU', 'Memory']);
    expect(within(sizes).getByText('16')).toBeTruthy();
    expect(within(sizes).getByText('32 GiB')).toBeTruthy();
    const backends = await screen.findByRole('table', { name: 'Backends' });
    expect(
      within(backends)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Backend', 'Allowed', 'Version']);
    const order = [
      sizes,
      backends,
      await screen.findByRole('region', { name: 'Demand' }),
    ];
    for (let i = 1; i < order.length; i++)
      expect(
        order[i - 1]!.compareDocumentPosition(order[i]!) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
  });
});
