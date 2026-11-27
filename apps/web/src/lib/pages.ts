// The pages of the site (website R5.1) with their head data (R6.9).
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

export const PAGES: readonly PageMeta[] = [
  {
    path: '/',
    file: 'index.html',
    title: 'CI Runners Shared Pool',
    description:
      'Run GitHub Actions on your own computers. One pool of machines for all your repositories, on Windows, macOS and Linux.',
    indexed: true,
    jsonLd: true,
  },
  {
    path: '/download',
    file: 'download/index.html',
    title: 'Download the Controller - CI Runners Shared Pool',
    description:
      'Download the Controller for Windows, macOS or Linux, with the SHA-256 of every file.',
    indexed: true,
    jsonLd: true,
  },
  {
    path: '/404',
    file: '404.html',
    title: 'Page not found - CI Runners Shared Pool',
    description: 'This page is gone. Go to the home page or the docs.',
    indexed: false,
  },
];

export const canonical = (path: string): string =>
  path === '/' ? `${SITE}/` : `${SITE}${path}`;
