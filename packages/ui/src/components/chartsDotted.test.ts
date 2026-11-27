// CRSP362: every chart part has a dotted form under the Cool Terminal Graphs setting (UX-91, components 52 to 55 and 60).
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
    BarList: '.ds-bars__bar',
  };
  for (const [name, cls] of Object.entries(parts)) {
    it(`${name} has a dotted rule`, () => {
      const rules = css.split('}').filter((r) => r.includes(DOTTED) && r.includes(cls));
      expect(rules.length).toBeGreaterThan(0);
    });
  }
  it('line charts use round dots', () => {
    expect(css).toMatch(/stroke-dasharray: 0 \d+;\s*stroke-linecap: round/);
  });
  it('dots never touch level bars or segment meters', () => {
    const rules = css.split('}').filter((r) => r.includes(DOTTED));
    for (const r of rules) {
      expect(r).not.toMatch(/ds-level|ds-segs|ds-segmeter/);
    }
  });
});
