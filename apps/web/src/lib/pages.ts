// The pages of the site (website R5.1) with their head data (R6.9).
import type { DocPage } from './docs.ts';
import type { ReleaseData } from './release.ts';
import type { StudyExample } from './study.ts';

export const SITE = 'https://crsp.luismc.net';

export interface PageMeta {
  path: string;
  /** The file written under dist. */
  file: string;
  title: string;
  description: string;
  /** Pages the sitemap lists; the 404 page is not one. */
  indexed: boolean;
  /** Carries SoftwareApplication data (R6.9). */
  jsonLd?: boolean;
}

/** A screenshot of the console, taken by `pnpm --filter web shots` (R5.4). */
export interface ShotData {
  /** The file name under `content/shots/`, without the extension. */
  name: string;
  src: string;
  alt: string;
  width: number;
  height: number;
}

/** Everything a page is drawn from. Pages read this and nothing else (R5.8). */
export interface SiteData {
  release: ReleaseData;
  docs: readonly DocPage[];
  study: StudyExample;
  shots: readonly ShotData[];
}

const BRAND = 'CI Runners Shared Pool';

export const STATIC_PAGES: readonly PageMeta[] = [
  {
    path: '/',
    file: 'index.html',
    title: BRAND,
    description:
      'Run GitHub Actions on your own computers. One pool of machines for all your repositories, on Windows, macOS and Linux.',
    indexed: true,
    jsonLd: true,
  },
  {
    path: '/product',
    file: 'product/index.html',
    title: `What the pool does - ${BRAND}`,
    description:
      'What CI Runners Shared Pool does: one pool of your own machines for every repository, with a console on your network.',
    indexed: true,
  },
  {
    path: '/download',
    file: 'download/index.html',
    title: `Download the Controller - ${BRAND}`,
    description:
      'Download the Controller for Windows, macOS or Linux, with the SHA-256 of every file.',
    indexed: true,
    jsonLd: true,
  },
  {
    path: '/docs',
    file: 'docs/index.html',
    title: `Docs - ${BRAND}`,
    description: 'Read and search the documentation of CI Runners Shared Pool.',
    indexed: true,
  },
  {
    path: '/security',
    file: 'security/index.html',
    title: `Security - ${BRAND}`,
    description:
      'How to report a vulnerability, what is signed, and how to check a download: checksums, attestations and bills of materials.',
    indexed: true,
  },
  {
    path: '/404',
    file: '404.html',
    title: `Page not found - ${BRAND}`,
    description: 'This page is gone. Go to the home page or the docs.',
    indexed: false,
  },
];

/** At most about one sentence for a page description (R6.9). */
export function describe(text: string, fallback: string): string {
  const t = text.trim();
  if (t === '') return fallback;
  if (t.length <= 155) return t;
  const cut = t.slice(0, 155);
  return `${cut.slice(0, Math.max(cut.lastIndexOf(' '), 100))}...`;
}

export function docMeta(d: DocPage): PageMeta {
  return {
    path: d.path,
    file: `${d.path.slice(1)}/index.html`,
    title: `${d.title} - Docs - ${BRAND}`,
    description: describe(d.summary, `${d.title}, from the docs of ${BRAND}.`),
    indexed: true,
  };
}

/** Every page the build writes: the fixed pages and one page per published doc. */
export function pagesFor(data: Pick<SiteData, 'docs'>): PageMeta[] {
  return [...STATIC_PAGES, ...data.docs.map(docMeta)];
}

export const canonical = (path: string): string =>
  path === '/' ? `${SITE}/` : `${SITE}${path}`;
