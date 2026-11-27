// CRSP302: the segment meter (component 85, UX-91) and the dotted form of the spark.
import { cleanup, render, screen } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { LevelBar } from './Progress';
import { SegMeter, segmentFills } from './SegMeter';
import { Spark, sparkChars, sparkDots } from './Spark';

afterEach(cleanup);
const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');

describe('segmentFills', () => {
  it('draws 10 segments by default in steps of 5 %, with a half-filled segment', () => {
    expect(segmentFills(55)).toEqual([1, 1, 1, 1, 1, 0.5, 0, 0, 0, 0]);
    expect(segmentFills(0).every((f) => f === 0)).toBe(true);
    expect(segmentFills(100).every((f) => f === 1)).toBe(true);
  });
  it('rounds the level to the nearest 5 %', () => {
    expect(segmentFills(52)).toEqual(segmentFills(50));
    expect(segmentFills(53)).toEqual(segmentFills(55));
  });
  it('keeps the count between 3 and 20 and clamps the level', () => {
    expect(segmentFills(50, 1)).toHaveLength(3);
    expect(segmentFills(50, 99)).toHaveLength(20);
    expect(segmentFills(50, 20).every((f) => f === 0 || f === 1)).toBe(true);
    expect(segmentFills(-10)[0]).toBe(0);
    expect(segmentFills(500).every((f) => f === 1)).toBe(true);
  });
  it('partly fills a wide segment of a 3-segment meter', () => {
    const f = segmentFills(50, 3);
    expect(f[0]).toBe(1);
    expect(f[1]).toBeCloseTo(0.5, 5);
    expect(f[2]).toBe(0);
  });
});

describe('SegMeter', () => {
  it('is a meter with its value in words and decorative segments', () => {
    const { container } = render(<SegMeter label="Memory" value={55} />);
    const m = screen.getByRole('meter', { name: 'Memory' });
    expect(m.getAttribute('aria-valuenow')).toBe('55');
    expect(m.getAttribute('aria-valuetext')).toBe('55%');
    expect(m.getAttribute('data-level')).toBe('ok');
    expect(container.querySelectorAll('.ds-segs__cell')).toHaveLength(10);
    expect(container.querySelector('.ds-segs')?.getAttribute('aria-hidden')).toBe('true');
    expect(container.querySelector('[data-fill="part"]')).toBeTruthy();
  });
  it('turns amber at 75 % and red at 90 %', () => {
    render(<SegMeter label="a" value={80} />);
    render(<SegMeter label="b" value={95} />);
    expect(screen.getByRole('meter', { name: 'a' }).getAttribute('data-level')).toBe(
      'warn',
    );
    expect(screen.getByRole('meter', { name: 'b' }).getAttribute('data-level')).toBe(
      'bad',
    );
  });
});

describe('LevelBar in the theme', () => {
  it('carries the segments beside its bar, hidden outside the Cool Terminal theme', () => {
    const { container } = render(
      <LevelBar label="Disk" value={5} max={10} text="5 of 10" />,
    );
    expect(container.querySelectorAll('.ds-level__bar .ds-segs__cell')).toHaveLength(10);
    expect(css).toMatch(/\.ds-level__bar \.ds-segs \{\s*display: none;/);
    expect(css).toMatch(
      /\[data-theme-name='terminal'\] \.ds-level__bar \.ds-segs \{\s*display: flex;/,
    );
  });
});

describe('dotted graphs', () => {
  it('draws the spark in braille dots with the same eight levels', () => {
    expect(sparkDots([1, 2, 3, 4, 5, 6, 7, 8])).toBe('⢀⣀⣄⣤⣦⣶⣷⣿');
    expect(sparkDots([])).toBe('');
    expect(sparkChars([1, 8])).toBe('▁█');
  });
  it('renders both forms hidden from a screen reader and switches by data-graphs', () => {
    const { container } = render(<Spark label="Runs" values={[1, 5, 9]} />);
    expect(
      container.querySelector('.ds-spark__blocks')?.getAttribute('aria-hidden'),
    ).toBe('true');
    expect(container.querySelector('.ds-spark__dots')?.getAttribute('aria-hidden')).toBe(
      'true',
    );
    expect(screen.getByRole('img').getAttribute('aria-label')).toContain('lowest 1');
    expect(css).toContain("[data-graphs='dotted'] .ds-spark__blocks");
    // Dots change charts only: bars and meters keep their blocks (owner, 27 Nov).
    expect(css).not.toContain("[data-graphs='dotted'] .ds-segs__cell");
    expect(css).not.toContain("[data-graphs='dotted'] .ds-level__bar");
  });
});
