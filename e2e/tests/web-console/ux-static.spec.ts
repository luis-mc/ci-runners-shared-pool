// CRSP164 (design system UX-9): the app and the shared UI never call window.alert, confirm or prompt. A static
// scan of the sources (test files are not judged): a dialog that opened would be a source line this scan finds.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { expect, test } from '@playwright/test';

const ROOTS = ['apps/controller-console/src', 'packages/ui/src'];
const CALL =
  /(?<![\w.$])(?:window\s*\.\s*|globalThis\s*\.\s*)?(alert|confirm|prompt)\s*\(/;

function sources(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...sources(p));
    else if (/\.(ts|tsx|js|jsx)$/.test(name) && !/\.(test|spec)\.[tj]sx?$/.test(name))
      out.push(p);
  }
  return out;
}

/** Finds a call in code, ignoring comments and string literals. */
export function findDialogCalls(source: string): string[] {
  const code = source
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:\\])\/\/[^\n]*/g, '$1')
    .replace(/(['"`])(?:\\.|(?!\1)[^\\\n])*\1/g, '""');
  return code
    .split('\n')
    .map((line, i) => (CALL.test(line) ? `${i + 1}: ${line.trim()}` : ''))
    .filter(Boolean);
}

test('no window.alert, confirm or prompt in the app or the shared UI', () => {
  const found: string[] = [];
  for (const root of ROOTS)
    for (const file of sources(resolve(root)))
      for (const hit of findDialogCalls(readFileSync(file, 'utf8')))
        found.push(`${file}:${hit}`);
  expect(found).toEqual([]);
});

test('the scan sees a call and ignores a word in text', () => {
  expect(findDialogCalls("if (window.confirm('Sure?')) go();")).toHaveLength(1);
  expect(findDialogCalls('alert("x")')).toHaveLength(1);
  expect(findDialogCalls("const t = 'prompt(please)'; // alert(1)")).toEqual([]);
  expect(findDialogCalls('const q = await Prompt(a); popup.alert(b);')).toEqual([]);
});
