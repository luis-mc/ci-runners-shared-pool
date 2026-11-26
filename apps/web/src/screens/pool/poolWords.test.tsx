import { cleanup, render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { PoolCountTiles } from './PoolSection';
import { poolCountsSentence } from './poolWords';

afterEach(cleanup);

// R6.13: the four counts have a text equivalent in one polite live region, held to one announcement per 10 s by
// the shared LiveText part.
describe('pool counts as text', () => {
  it('says the four counts in one sentence', () => {
    expect(poolCountsSentence({ running: 2, starting: 0, queued: 3, noRunner: 1 })).toBe(
      '2 running, 0 starting, 3 queued, 1 with no runner that fits',
    );
  });

  it('puts the sentence in one polite live region beside the tiles, and none while loading', () => {
    const { container, rerender } = render(
      <MemoryRouter>
        <PoolCountTiles counts={undefined} />
      </MemoryRouter>,
    );
    expect(container.querySelectorAll('[aria-live]')).toHaveLength(0);
    rerender(
      <MemoryRouter>
        <PoolCountTiles counts={{ running: 2, starting: 0, queued: 3, noRunner: 1 }} />
      </MemoryRouter>,
    );
    const regions = container.querySelectorAll('[data-live-text]');
    expect(regions).toHaveLength(1);
    expect(regions[0]!.textContent).toBe(
      '2 running, 0 starting, 3 queued, 1 with no runner that fits',
    );
  });
});
