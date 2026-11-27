import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

/**
 * OPS35 guard: the web console holds no absolute http(s) address in code.
 *
 * The console calls the controller on its own origin (baseUrl ''), and every
 * GitHub page it opens comes from the controller, which builds it from
 * CRSP_GITHUB_URL. A host written into a string here would be a second
 * setting nobody can change.
 *
 * It reads each non-test source file under apps/controller-console/src and packages/<name>/src
 * with the TypeScript parser, so comments are never scanned. The words a
 * person reads (help text) may show a documentation host (example.com) or this
 * PC (127.0.0.1, localhost): those never name a host crsp talks to.
 */

const ROOT = join(__dirname, '..');
const ROOTS = [
  'apps/controller-console/src',
  ...readdirSync(join(ROOT, 'packages')).map((p) => `packages/${p}/src`),
].filter((r) => existsSync(join(ROOT, r)));
const ADDRESS = /https?:\/\/([A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?)/g;
const SKIP_DIRS = new Set(['node_modules', 'testing', 'gen', 'generated']);

function allowedHost(host: string): boolean {
  return (
    host === '127.0.0.1' ||
    host === 'localhost' ||
    host === 'example.com' ||
    host.endsWith('.example.com')
  );
}

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) {
      if (!SKIP_DIRS.has(e.name)) out.push(...sourceFiles(p));
    } else if (
      /\.(ts|tsx)$/.test(e.name) &&
      !/\.(test|spec|guard\.test)\.tsx?$/.test(e.name) &&
      !e.name.endsWith('.d.ts')
    ) {
      out.push(p);
    }
  }
  return out;
}

/** Every text the code holds: strings, templates and JSX text. Comments are not nodes. */
export function textsOf(file: string, source: string): { text: string; line: number }[] {
  const sf = ts.createSourceFile(
    file,
    source,
    ts.ScriptTarget.Latest,
    true,
    file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  const out: { text: string; line: number }[] = [];
  const visit = (n: ts.Node): void => {
    if (
      ts.isStringLiteral(n) ||
      ts.isNoSubstitutionTemplateLiteral(n) ||
      ts.isTemplateHead(n) ||
      ts.isTemplateMiddle(n) ||
      ts.isTemplateTail(n) ||
      ts.isJsxText(n)
    ) {
      out.push({
        text: n.text,
        line: sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1,
      });
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

export function addressProblems(file: string, source: string): string[] {
  const problems: string[] = [];
  for (const { text, line } of textsOf(file, source)) {
    for (const m of text.matchAll(ADDRESS)) {
      if (!allowedHost(m[1] ?? ''))
        problems.push(`${file}:${line}: absolute address ${m[0]}`);
    }
  }
  return problems;
}

describe('no hardcoded addresses in the web console', () => {
  it('holds no absolute http(s) address in code', () => {
    const problems = ROOTS.flatMap((r) =>
      sourceFiles(join(ROOT, r)).flatMap((f) =>
        addressProblems(relative(ROOT, f), readFileSync(f, 'utf8')),
      ),
    );
    expect(problems).toEqual([]);
  });

  it('sees a host in a string, a template and JSX text, and not in a comment', () => {
    const src = [
      '// https://comment.example.org/ignored',
      "const a = 'https://github.com/x';",
      'const b = `https://api.github.com/${a}`;',
      'const c = <p>Open http://controller.lan:8460 now</p>;',
      "const d = 'http://127.0.0.1:8460 and https://hooks.example.com';",
    ].join('\n');
    expect(addressProblems('x.tsx', src)).toEqual([
      'x.tsx:2: absolute address https://github.com',
      'x.tsx:3: absolute address https://api.github.com',
      'x.tsx:4: absolute address http://controller.lan',
    ]);
  });
});
