// Component 78: the summary line says its counts on one line, number first.
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { SummaryLine } from './SummaryLine';

afterEach(cleanup);

describe('SummaryLine', () => {
  it('says the totals, a dash, then the states', () => {
    const { container } = render(
      <SummaryLine
        total={[
          { count: 1, word: 'machine', plural: 'machines' },
          { count: 5, word: 'runner', plural: 'runners' },
        ]}
        states={[
          { count: 4, word: 'idle' },
          { count: 0, word: 'busy' },
          { count: 1, word: 'not ready', tone: 'warning' },
        ]}
      />,
    );
    expect(container.textContent).toBe(
      '1 machine · 5 runners — 4 idle · 0 busy · 1 not ready',
    );
  });

  it('colours only a count above zero, and dims a zero', () => {
    const { container } = render(
      <SummaryLine
        total={[{ count: 2, word: 'runner', plural: 'runners' }]}
        states={[
          { count: 0, word: 'not ready', tone: 'warning' },
          { count: 2, word: 'failed', tone: 'danger' },
          { count: 3, word: 'idle' },
        ]}
      />,
    );
    const items = [...container.querySelectorAll('.ds-summary__item')];
    expect(items[1]!.hasAttribute('data-zero')).toBe(true);
    expect(items[1]!.getAttribute('data-tone')).toBeNull();
    expect(items[2]!.getAttribute('data-tone')).toBe('danger');
    expect(items[3]!.getAttribute('data-tone')).toBeNull();
  });

  it('leaves out the dash without states', () => {
    const { container } = render(
      <SummaryLine total={[{ count: 0, word: 'runner', plural: 'runners' }]} />,
    );
    expect(container.textContent).toBe('0 runners');
  });
});
