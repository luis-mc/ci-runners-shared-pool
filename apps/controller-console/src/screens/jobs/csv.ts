// A CSV file of rows, for the Export button. Text that starts like a formula is made harmless (UX-39), and a
// field with a comma, a quote or a line break is quoted.
import type { ExportedFile } from '@crsp/ui';

const field = (v: string | number | undefined): string => {
  let s = v === undefined ? '' : String(v);
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`;
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
};

export function csvFile(
  name: string,
  head: readonly string[],
  rows: readonly (string | number | undefined)[][],
): ExportedFile {
  const text = [head, ...rows].map((r) => r.map(field).join(',')).join('\n');
  return { blob: new Blob([`${text}\n`], { type: 'text/csv' }), filename: name };
}
