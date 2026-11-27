// CRSP369: every bar-like part draws the theme's blocks in the Cool Terminal themes (UX-91, shared Segments).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const dir = import.meta.dirname;
const css = readFileSync(join(dir, '..', 'components.css'), 'utf8');
const src = (f: string) => readFileSync(join(dir, f), 'utf8');
const TERMINAL = "[data-theme-name='terminal']";

/** Every part that draws a bar or a level, with the class of its plain bar, the file that draws it and the segments' home. */
const BAR_PARTS: { part: string; plain: string; file: string }[] = [
  {
    part: 'LevelBar (component 16 level)',
    plain: '.ds-level__fill',
    file: 'Progress.tsx',
  },
  {
    part: 'Progress and PhaseProgress (component 16)',
    plain: '.ds-progress__fill',
    file: 'Progress.tsx',
  },
  { part: 'BarList (component 60)', plain: '.ds-bars__bar', file: 'BarList.tsx' },
  { part: 'SegMeter (component 85)', plain: '.ds-segmeter', file: 'SegMeter.tsx' },
];

describe('bars draw segments in the Cool Terminal themes', () => {
  for (const b of BAR_PARTS)
    it(`${b.part} has a terminal rule and draws Segments`, () => {
      const rules = css
        .split('}')
        .filter((r) => r.includes(TERMINAL) && r.includes(b.plain.split(' ')[0] ?? ''));
      if (b.plain !== '.ds-segmeter') expect(rules.length).toBeGreaterThan(0);
      expect(src(b.file)).toMatch(/Segments/);
    });
  it('the wizard, the page strip and the loading state use the shared Progress', () => {
    for (const f of ['Wizard.tsx', 'Shell.tsx', 'State.tsx', 'Lifecycle.tsx'])
      expect(src(f)).toMatch(/Progress/);
  });
  it('Dots leaves every bar as blocks (the bar list too, owner 27 Nov)', () => {
    const dotted = css.split('}').filter((r) => r.includes("[data-graphs='dotted']"));
    for (const r of dotted) expect(r).not.toMatch(/ds-progress|ds-level|ds-segs|ds-bars/);
  });
});
