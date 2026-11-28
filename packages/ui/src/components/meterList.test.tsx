// CRSP395 (UX-92, component 92): the meter list is one grid; its rows split the value so numbers and "of" words line up.
import { cleanup, render, screen } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { BarList } from './BarList';
import { Meter } from './Meter';
import { LevelBar, MeterList, splitLevelText } from './Progress';

afterEach(cleanup);
const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');

describe('splitLevelText', () => {
  it('splits at the first " of " so the number and the words after it have their own columns', () => {
    expect(splitLevelText('3.7 of 32')).toEqual({ used: '3.7', rest: 'of 32' });
    expect(splitLevelText('19.5 of 31.7 GiB')).toEqual({
      used: '19.5',
      rest: 'of 31.7 GiB',
    });
    expect(splitLevelText('1.0 GB of 8.0 GB used')).toEqual({
      used: '1.0 GB',
      rest: 'of 8.0 GB used',
    });
  });
  it('keeps words without "of" whole', () => {
    expect(splitLevelText('not known')).toEqual({ used: 'not known', rest: '' });
  });
});

describe('MeterList', () => {
  it('holds every row in one grid, each row with the same parts and the same segment count', () => {
    const { container } = render(
      <MeterList>
        <LevelBar label="CPU" value={3.7} max={32} text="3.7 of 32" />
        <LevelBar label="Memory" value={19.5} max={31.7} text="19.5 of 31.7 GiB" />
        <LevelBar label="Swap" value={0} max={1} text="not known" />
      </MeterList>,
    );
    const grid = container.querySelector('.ds-meters__grid');
    const rows = container.querySelectorAll('.ds-meters__grid > .ds-level');
    expect(rows).toHaveLength(3);
    expect(grid?.parentElement?.className).toBe('ds-meters');
    const counts = Array.from(rows).map(
      (r) => r.querySelectorAll('.ds-segs__cell').length,
    );
    expect(new Set(counts)).toEqual(new Set([10]));
    expect(container.querySelectorAll('.ds-level__used')).toHaveLength(2);
    expect(container.querySelectorAll('.ds-level__rest')).toHaveLength(2);
    expect(container.querySelectorAll('.ds-level__text')).toHaveLength(1);
  });

  it('keeps the whole value for a screen reader and hides the split copy', () => {
    render(
      <MeterList>
        <LevelBar label="Memory" value={1} max={2} text="19.5 of 31.7 GiB" />
      </MeterList>,
    );
    expect(screen.getByText('19.5 of 31.7 GiB').className).toContain(
      'ds-visually-hidden',
    );
    expect(
      screen.getByRole('meter', { name: 'Memory' }).getAttribute('aria-valuetext'),
    ).toBe('19.5 of 31.7 GiB');
    expect(document.querySelector('.ds-level__used')?.getAttribute('aria-hidden')).toBe(
      'true',
    );
  });

  it('draws the grid from shared tracks, in tabular figures, and stacks in a narrow box', () => {
    expect(css).toMatch(/\.ds-meters \{\s*container-type: inline-size;/);
    expect(css).toMatch(/--ds-meter-cols: fit-content\(14rem\) minmax\(6rem, 28rem\)/);
    expect(css).toMatch(
      /\.ds-level \{\s*display: grid;\s*grid-template-columns: subgrid;/,
    );
    expect(css).toContain('font-variant-numeric: tabular-nums');
    expect(css).toContain('@container (max-inline-size: 26rem)');
    expect(css).toMatch(/\.ds-level__used \{\s*text-align: end;/);
  });
});

describe('one bar style', () => {
  it('draws Meter with the shared segments', () => {
    const { container } = render(<Meter label="Warmth" value={50} cells={5} />);
    expect(container.querySelectorAll('.ds-segs__cell')).toHaveLength(5);
  });
  it('lays the bar list out on the same grid inside a meters box', () => {
    const { container } = render(
      <MemoryRouter>
        <BarList
          label="Busiest"
          items={[
            {
              key: 'a',
              label: 'octo/app',
              value: 78,
              text: '78 jobs',
              linkName: 'Open a',
              to: '/a',
            },
            {
              key: 'b',
              label: 'octo/docs',
              value: 6,
              text: '6 jobs',
              linkName: 'Open b',
              to: '/b',
            },
          ]}
        />
      </MemoryRouter>,
    );
    expect(container.querySelector('.ds-meters > .ds-bars')).toBeTruthy();
    expect(css).toMatch(
      /\.ds-bars__row \{\s*display: grid;\s*grid-template-columns: subgrid;/,
    );
  });
});
