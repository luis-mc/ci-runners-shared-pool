// A static file server for the browser tests: it serves apps/web/dist the way Vercel does (vercel.json: clean URLs and
// the response headers; 404.html for an unknown path), so a header or a route check here holds in production.
import { readFileSync, statSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import { extname, join, normalize, resolve } from 'node:path';

const TYPES: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.txt': 'text/plain; charset=utf-8',
  '.xml': 'application/xml; charset=utf-8',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
};

interface VercelConfig {
  headers: { source: string; headers: { key: string; value: string }[] }[];
}

export function siteHeaders(root: string): Record<string, string> {
  const config = JSON.parse(
    readFileSync(join(root, 'vercel.json'), 'utf8'),
  ) as VercelConfig;
  const out: Record<string, string> = {};
  for (const rule of config.headers)
    if (rule.source === '/(.*)') for (const h of rule.headers) out[h.key] = h.value;
  return out;
}

const isFile = (p: string): boolean => {
  try {
    return statSync(p).isFile();
  } catch {
    return false;
  }
};

export function startServer(root: string, dist: string, port = 0): Promise<Server> {
  const headers = siteHeaders(root);
  const server = createServer((req, res) => {
    const path = decodeURIComponent((req.url ?? '/').split('?')[0]!);
    const rel = normalize(path).replace(/^[/\\]+/, '');
    const base = resolve(dist, rel);
    if (!base.startsWith(resolve(dist))) {
      res.writeHead(403).end();
      return;
    }
    const candidates = [base, `${base}.html`, join(base, 'index.html')];
    const file = candidates.find(isFile);
    const status = file ? 200 : 404;
    const body = readFileSync(file ?? join(dist, '404.html'));
    res.writeHead(status, {
      ...headers,
      'content-type': TYPES[extname(file ?? '404.html')] ?? 'application/octet-stream',
    });
    res.end(body);
  });
  return new Promise((ok) => server.listen(port, '127.0.0.1', () => ok(server)));
}
