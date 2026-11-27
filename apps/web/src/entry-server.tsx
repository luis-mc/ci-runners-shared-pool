// Server entry: renders a page to static HTML (website R6.2, R6.6). The build calls `render` once per page.
import { renderToStaticMarkup } from 'react-dom/server';
import { StaticRouter } from 'react-router-dom';
import { Download, Home, NotFound } from './pages.tsx';
import type { ReleaseData } from './lib/release.ts';

export function render(path: string, release: ReleaseData): string {
  const page =
    path === '/' ? (
      <Home />
    ) : path === '/download' ? (
      <Download release={release} />
    ) : (
      <NotFound />
    );
  return renderToStaticMarkup(<StaticRouter location={path}>{page}</StaticRouter>);
}
