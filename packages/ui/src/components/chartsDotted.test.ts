// @vitest-environment jsdom
// CRSP362, CRSP370: every chart part has a dotted form of solid round filled dots under the Cool Terminal Graphs setting (UX-91, components 52 to 55).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cleanup, render } from '@testing-library/react';
import { createElement } from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { TrendChart } from './Charts';
import { boxDots, GRID_GAP, GRID_R } from './dots';

const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');
const DOTTED = "[data-theme-name='terminal'][data-graphs='dotted']";

describe('dotted charts', () => {
  const parts: Record<string, string> = {
    'TrendChart line': '.ds-trend__line',
    'TrendChart area': '.ds-trend__band',
    'TrendChart column': '.ds-trend__column',
    Sparkline: '.ds-spark__line',
    Heatmap: '.ds-heat__cell',
    ForecastChart: '.ds-fchart__diagonal',
  };
  for (const [name, cls] of Object.entries(parts)) {
    it(`${name} has a dotted rule`, () => {
      const rules = css.split('}').filter((r) => r.includes(DOTTED) && r.includes(cls));
      expect(rules.length).toBeGreaterThan(0);
    });
  }
  it('the dots are filled circles, not an outline over a wash (CRSP370)', () => {
    expect(css).toMatch(/\.ds-dot \{\s*fill: var\(--ds-color-primary\)/);
    const dotted = css.split('}').filter((r) => r.includes(DOTTED));
    for (const r of dotted) {
      expect(r).not.toMatch(/fill-opacity: 0\.18/);
      expect(r).not.toMatch(/ds-trend__column[^{]*\{[^}]*stroke-dasharray/);
    }
    // The line, area and column are replaced by dots drawn in the part, so the stroke form hides.
    const hide = css
      .split('}')
      .find((r) => r.includes(DOTTED) && r.includes('.ds-trend__column'));
    expect(hide).toMatch(/display: none/);
  });
  it('the chart parts draw circles themselves', () => {
    const charts = readFileSync(join(import.meta.dirname, 'Charts.tsx'), 'utf8');
    expect(charts).toContain('DotLayer');
    expect(charts.match(/<DotLayer/g)?.length).toBeGreaterThanOrEqual(3);
  });
  it('dots never touch a bar: level bars, segment meters, progress bars, the bar list', () => {
    const rules = css.split('}').filter((r) => r.includes(DOTTED));
    for (const r of rules) {
      expect(r).not.toMatch(/ds-level|ds-segs|ds-segmeter|ds-progress|ds-bars/);
    }
  });
});

afterEach(() => {
  cleanup();
  document.documentElement.removeAttribute('data-theme-name');
  document.documentElement.removeAttribute('data-graphs');
});

describe('a dotted column has the box of the standard bar (CRSP374)', () => {
  const series = [
    {
      id: 'jobs',
      label: 'Jobs',
      kind: 'column' as const,
      points: [
        { x: 'Mon', y: 4 },
        { x: 'Tue', y: 10 },
        { x: 'Wed', y: 7 },
        { x: 'Thu', y: 0.4 },
      ],
    },
  ];
  const draw = () =>
    render(
      createElement(TrendChart, {
        label: 'Jobs per day',
        series,
        unit: 'count',
        emptyText: 'none',
      }),
    );
  // The jsdom plot is not measured, so the dot layer uses its 600 by 200 default: one SVG unit is one pixel.
  const num = (v: string | null) => Number.parseFloat(v ?? '0');

  it('every dotted bar covers the filled bar within 1 px', () => {
    const plain = draw();
    const bars = [...plain.container.querySelectorAll('.ds-trend__column')].map((r) => ({
      x: num(r.getAttribute('x')),
      y: num(r.getAttribute('y')),
      w: num(r.getAttribute('width')),
      h: num(r.getAttribute('height')),
    }));
    cleanup();
    document.documentElement.setAttribute('data-theme-name', 'terminal');
    document.documentElement.setAttribute('data-graphs', 'dotted');
    const dotted = draw();
    const dots = [...dotted.container.querySelectorAll('.ds-dot')].map((c) => ({
      cx: (num(c.getAttribute('cx')) / 100) * 600,
      cy: (num(c.getAttribute('cy')) / 100) * 200,
      r: num(c.getAttribute('r')),
    }));
    expect(bars.length).toBe(4);
    for (const b of bars) {
      const inside = dots.filter(
        (d) =>
          d.cx >= b.x - 1 &&
          d.cx <= b.x + b.w + 1 &&
          d.cy >= b.y - 1 &&
          d.cy <= b.y + b.h + 1,
      );
      expect(inside.length).toBeGreaterThan(0);
      const left = Math.min(...inside.map((d) => d.cx - d.r));
      const right = Math.max(...inside.map((d) => d.cx + d.r));
      const top = Math.min(...inside.map((d) => d.cy - d.r));
      const bottom = Math.max(...inside.map((d) => d.cy + d.r));
      expect(Math.abs(top - b.y)).toBeLessThanOrEqual(1);
      expect(Math.abs(bottom - (b.y + b.h))).toBeLessThanOrEqual(1);
      // A bar thinner than a dot is one dot wide, so its box can only be as wide as that dot.
      if (b.w >= 2 * GRID_R) {
        expect(Math.abs(left - b.x)).toBeLessThanOrEqual(1);
        expect(Math.abs(right - (b.x + b.w))).toBeLessThanOrEqual(1);
      }
    }
  });

  it('boxDots fills any box exactly, with several dots across a wide one', () => {
    for (const [w, h] of [
      [60, 100],
      [14, 33],
      [3, 50],
      [40, 1.5],
    ] as const) {
      const d = boxDots(10, 20, w, h);
      expect(d.length).toBeGreaterThan(0);
      expect(Math.abs(Math.min(...d.map((p) => p.cy - p.r)) - 20)).toBeLessThanOrEqual(
        0.01,
      );
      expect(
        Math.abs(Math.max(...d.map((p) => p.cy + p.r)) - (20 + h)),
      ).toBeLessThanOrEqual(0.01);
    }
    const across = new Set(boxDots(0, 0, 60, 30).map((p) => p.cx)).size;
    expect(across).toBe(Math.floor((60 + GRID_GAP) / (2 * GRID_R + GRID_GAP)));
  });

  it('the dot size and gap are the tokens', () => {
    const tokens = readFileSync(join(import.meta.dirname, '..', 'tokens.css'), 'utf8');
    expect(tokens).toContain(`--ds-dot-r: ${GRID_R}px;`);
    expect(tokens).toContain(`--ds-dot-gap: ${GRID_GAP}px;`);
  });
});
