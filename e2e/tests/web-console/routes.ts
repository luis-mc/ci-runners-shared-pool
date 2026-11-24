// The console's route table, read from apps/web/src/App.tsx, so a screen added to the table is covered by the
// checks that use this list (axe, offline) without anyone editing a list here (R6.6, R6.13).
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

export interface ConsoleRoute {
  /** The route pattern, for example `/setup/pool/:owner/:name`. */
  pattern: string;
  /** A URL that opens it: the parameters are filled with the ids of the mocked data. */
  url: string;
  /** Sign-in is shown to a signed-out visitor; every other screen needs a session. */
  signedOut: boolean;
}

const SAMPLE: Record<string, string> = {
  owner: 'octo',
  name: 'app',
  id: '1',
  imageId: 'img_m1_linux',
  machineId: 'm1',
};
export const NOT_FOUND_URL = '/no/such/page';

/** Scans `<Route ...>` tags; the attribute text may hold `>` inside braces, so braces are counted. */
export function parseRoutes(source: string): ConsoleRoute[] {
  const out: ConsoleRoute[] = [];
  const stack: string[] = [];
  const re = /<Route\b|<\/Route>/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(source))) {
    if (m[0] === '</Route>') {
      stack.pop();
      continue;
    }
    let i = m.index + m[0].length;
    let depth = 0;
    let end = -1;
    for (; i < source.length; i++) {
      const c = source[i];
      if (c === '{') depth++;
      else if (c === '}') depth--;
      else if (c === '>' && depth === 0) {
        end = i;
        break;
      }
    }
    if (end < 0) throw new Error('App.tsx: an unterminated <Route> tag');
    const tag = source.slice(m.index, end + 1);
    const selfClosing = tag.endsWith('/>');
    const path = /\bpath="([^"]*)"/.exec(tag)?.[1];
    const isIndex = /\bindex\b/.test(tag.replace(/element=\{[\s\S]*\}/, ''));
    const segment = path ?? '';
    // A route whose path is a computed value (the moved-address redirects in App.tsx) is a redirect, not a screen.
    if (selfClosing && path === undefined && /\bpath=\{/.test(tag)) continue;
    if (!selfClosing) {
      stack.push(segment);
      continue;
    }
    if (segment === '*' && stack.length > 0) continue; // the not-found route: NOT_FOUND_URL covers it
    if (isIndex && /<Navigate\b/.test(tag)) continue; // a redirect is not a screen
    const full = [...stack, segment]
      .join('/')
      .replace(/\/\*$/, '')
      .split('/')
      .filter(Boolean);
    // /setup is the first-run page only with a first-run parameter (web-console 4.3a); without one it opens Setup.
    const url =
      '/' +
      full.map((p) => (p.startsWith(':') ? (SAMPLE[p.slice(1)] ?? 'x') : p)).join('/') +
      (full.join('/') === 'setup' ? '?wizard=1' : '');
    out.push({
      pattern: '/' + full.join('/'),
      url,
      signedOut: url === '/sign-in',
    });
  }
  return out;
}

export function consoleRoutes(): ConsoleRoute[] {
  // The Jobs section keeps its routes in screens/jobs/routes.tsx; App.tsx places them as `{jobsRoutes}`.
  const jobs = readFileSync(resolve('apps/web/src/screens/jobs/routes.tsx'), 'utf8');
  const jobsMarkup = /<>([\s\S]*)<\/>/.exec(jobs)?.[1] ?? '';
  if (!jobsMarkup.includes('<Route'))
    throw new Error('screens/jobs/routes.tsx: no routes found; fix routes.ts');
  const source = readFileSync(resolve('apps/web/src/App.tsx'), 'utf8').replace(
    '{jobsRoutes}',
    jobsMarkup,
  );
  const routes = parseRoutes(source);
  // A parser that finds nothing would make every check pass on an empty list.
  if (routes.length < 10)
    throw new Error(`App.tsx: only ${routes.length} routes found; fix routes.ts`);
  return routes;
}
