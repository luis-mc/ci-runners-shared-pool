// The savings example (website R5.5): every number about cost comes from section 6 of the runs-on study, with its
// assumptions and its date. The build reads the study and fails when the section changes shape, so a number can never
// be typed on the site by hand.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

export interface StudyExample {
  /** For example "10,000 minutes a month of 4-vCPU Linux jobs". */
  workload: string;
  /** When the vendor rates were read, for example "25 Nov 2026". */
  ratesRead: string;
  /** The monthly total for each way of running the jobs, as the study states it. */
  totals: { name: string; total: string }[];
  /** The study's own reading of the table. */
  reading: string;
}

const section6 = (md: string): string => {
  const m = /^## 6\. Worked cost example\r?\n([\s\S]*?)(?=^## 7\.)/m.exec(md);
  if (!m) throw new Error('the study has no "## 6. Worked cost example" section');
  return m[1]!;
};

/** Reads the example out of section 6 of the study. Throws when a part is missing. */
export function parseStudy(md: string): StudyExample {
  const s = section6(md);
  const workload = /^- (\d[\d,]* minutes a month of [^(\n]+?)(?: \(|\.\s*$)/m
    .exec(s)?.[1]
    ?.trim();
  const ratesRead = /rates from the runs-on pricing page, read (\d+ \w+ \d{4})/i.exec(
    s,
  )?.[1];
  const rows = s
    .split(/\r?\n/)
    .filter((l) => l.startsWith('|'))
    .map((l) =>
      l
        .split('|')
        .slice(1, -1)
        .map((c) => c.replace(/\*\*/g, '').trim()),
    );
  const head = rows.find((r) => r[0] === 'Per month');
  const total = rows.find((r) => r[0] === 'Total');
  const reading = /^Reading: (.+)$/m.exec(s)?.[1]?.trim();
  if (!workload) throw new Error('the study example has no workload line');
  if (!ratesRead) throw new Error('the study example does not say when rates were read');
  if (!head || !total || head.length < 2 || head.length !== total.length)
    throw new Error('the study example has no Per month and Total rows');
  if (!reading) throw new Error('the study example has no Reading line');
  return {
    workload,
    ratesRead,
    totals: head.slice(1).map((name, i) => ({ name, total: total[i + 1]! })),
    reading,
  };
}

export function loadStudy(repo: string): StudyExample {
  return parseStudy(
    readFileSync(join(repo, 'docs', 'studies', 'runs-on-vs-crsp', 'study.md'), 'utf8'),
  );
}
