// The savings example is read from the study (website R5.5).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { parseStudy } from './study.ts';

const study = readFileSync(
  join(
    import.meta.dirname,
    '..',
    '..',
    '..',
    '..',
    'docs',
    'studies',
    'runs-on-vs-crsp',
    'study.md',
  ),
  'utf8',
);

describe('study example', () => {
  it('reads the workload, the date, the totals and the reading', () => {
    const e = parseStudy(study);
    expect(e.workload).toBe('10,000 minutes a month of 4-vCPU Linux jobs');
    expect(e.ratesRead).toBe('25 Nov 2026');
    expect(e.totals).toEqual([
      { name: 'GitHub-hosted', total: '$120' },
      { name: 'runs-on', total: 'about $41' },
      { name: 'CRSP', total: '$0' },
    ]);
    expect(e.reading).toMatch(/^runs-on cuts/);
  });
  it('fails when the section changes shape', () => {
    expect(() => parseStudy('# x\n')).toThrow(/Worked cost example/);
    expect(() => parseStudy('## 6. Worked cost example\n\ntext\n\n## 7. Next\n')).toThrow(
      /workload/,
    );
  });
});
