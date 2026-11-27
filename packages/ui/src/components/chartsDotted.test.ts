// CRSP362, CRSP370: every chart part has a dotted form of solid round filled dots under the Cool Terminal Graphs setting (UX-91, components 52 to 55).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

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
