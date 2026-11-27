// The pages the browser tests visit: every fixed page, a short doc, a long doc with a diagram and a table, and the 404.
export const PAGES = [
  '/',
  '/product',
  '/download',
  '/docs',
  '/security',
  '/docs/guides/install',
  '/docs/features/website',
  '/docs/design-system/next-version/tokens',
  '/no-such-page',
] as const;

export const WIDTHS = [360, 393, 1300] as const;
