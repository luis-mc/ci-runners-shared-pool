// Builds the site into apps/web/dist (website R6.6, R6.7): release data, one client build (stylesheet and the system
// picker), one server build that renders each page to static HTML. Run by `pnpm --filter web build` and by Vercel.
// `CRSP_RELEASE_FILE` points at a release.json to use instead of reading GitHub (tests, offline builds).
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { build } from 'vite';
import { canonical, PAGES, SITE, type PageMeta } from '../src/lib/pages.ts';
import { fetchRelease, type ReleaseData } from '../src/lib/release.ts';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

interface ManifestEntry {
  file: string;
  css?: string[];
  isEntry?: boolean;
}

const escape = (s: string): string =>
  s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');

const jsonLd = (page: PageMeta): string =>
  JSON.stringify({
    '@context': 'https://schema.org',
    '@type': 'SoftwareApplication',
    name: 'CI Runners Shared Pool',
    applicationCategory: 'DeveloperApplication',
    operatingSystem: 'Windows, macOS, Linux',
    license: 'https://opensource.org/license/mit',
    url: canonical(page.path),
    offers: { '@type': 'Offer', price: '0', priceCurrency: 'USD' },
  }).replace(/</g, '\\u003c');

export function pageHtml(page: PageMeta, body: string, entry: ManifestEntry): string {
  const css = (entry.css ?? [])
    .map((f) => `<link rel="stylesheet" href="/${f}">`)
    .join('');
  return [
    '<!doctype html>',
    '<html lang="en" data-theme-name="phosphor" data-theme="dark"><head>',
    '<meta charset="utf-8">',
    '<meta name="viewport" content="width=device-width, initial-scale=1">',
    '<script src="/theme-init.js"></script>',
    '<link rel="icon" type="image/png" href="/crsp-icon.png">',
    `<title>${escape(page.title)}</title>`,
    `<meta name="description" content="${escape(page.description)}">`,
    page.indexed
      ? `<link rel="canonical" href="${canonical(page.path)}">`
      : '<meta name="robots" content="noindex">',
    css,
    page.jsonLd ? `<script type="application/ld+json">${jsonLd(page)}</script>` : '',
    `<script type="module" src="/${entry.file}"></script>`,
    '</head><body>',
    body,
    '</body></html>',
  ].join('');
}

export const sitemap = (): string =>
  `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${PAGES.filter(
    (p) => p.indexed,
  )
    .map((p) => `  <url><loc>${canonical(p.path)}</loc></url>`)
    .join('\n')}\n</urlset>\n`;

export async function buildSite(outDir: string, release: ReleaseData): Promise<void> {
  await build({
    root,
    configFile: join(root, 'vite.config.ts'),
    logLevel: 'silent',
    build: { outDir, emptyOutDir: true },
  });
  const manifest = JSON.parse(
    readFileSync(join(outDir, '.vite', 'manifest.json'), 'utf8'),
  ) as Record<string, ManifestEntry>;
  const found = Object.values(manifest).find((m) => m.isEntry);
  // With one stylesheet (cssCodeSplit off) vite lists it as its own manifest entry, not under the script.
  const sheets = Object.values(manifest)
    .filter((m) => m.file.endsWith('.css'))
    .map((m) => m.file);
  const entry = found ? { ...found, css: [...(found.css ?? []), ...sheets] } : undefined;
  if (!entry) throw new Error('The client build has no entry');

  const ssrDir = mkdtempSync(join(root, 'node_modules', '.crsp-ssr-'));
  try {
    await build({
      root,
      configFile: join(root, 'vite.config.ts'),
      logLevel: 'silent',
      build: {
        ssr: join(root, 'src', 'entry-server.tsx'),
        outDir: ssrDir,
        emptyOutDir: true,
        manifest: false,
        rollupOptions: { input: join(root, 'src', 'entry-server.tsx') },
      },
      ssr: { noExternal: [/^@crsp\//] },
    });
    const server = (await import(
      pathToFileURL(join(ssrDir, 'entry-server.js')).href
    )) as {
      render: (path: string, release: ReleaseData) => string;
    };
    for (const page of PAGES) {
      const file = join(outDir, page.file);
      mkdirSync(dirname(file), { recursive: true });
      writeFileSync(file, pageHtml(page, server.render(page.path, release), entry));
    }
  } finally {
    rmSync(ssrDir, { recursive: true, force: true });
  }
  rmSync(join(outDir, '.vite'), { recursive: true, force: true });
  writeFileSync(join(outDir, 'sitemap.xml'), sitemap());
  writeFileSync(
    join(outDir, 'robots.txt'),
    `User-agent: *\nAllow: /\nSitemap: ${SITE}/sitemap.xml\n`,
  );
  writeFileSync(join(outDir, 'release.json'), `${JSON.stringify(release, null, 2)}\n`);
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const file = process.env['CRSP_RELEASE_FILE'];
  const release = file
    ? (JSON.parse(readFileSync(file, 'utf8')) as ReleaseData)
    : await fetchRelease();
  await buildSite(join(root, 'dist'), release);
  console.log(`site built: release ${release.tag}, ${PAGES.length} pages`);
}
