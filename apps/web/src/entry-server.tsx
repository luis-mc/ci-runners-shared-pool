// Server entry: renders a page to static HTML (website R6.2, R6.6). The build calls `render` once per page.
import { renderToStaticMarkup } from 'react-dom/server';
import { StaticRouter } from 'react-router-dom';
import { Doc, Docs, Download, Home, NotFound, Product, Security } from './pages.tsx';
import type { SiteData } from './lib/pages.ts';

export function render(path: string, data: SiteData): string {
  const doc = data.docs.find((d) => d.path === path);
  const page = doc ? (
    <Doc page={doc} />
  ) : path === '/' ? (
    <Home data={data} />
  ) : path === '/product' ? (
    <Product data={data} />
  ) : path === '/download' ? (
    <Download release={data.release} />
  ) : path === '/docs' ? (
    <Docs docs={data.docs} />
  ) : path === '/security' ? (
    <Security release={data.release} />
  ) : (
    <NotFound />
  );
  return renderToStaticMarkup(<StaticRouter location={path}>{page}</StaticRouter>);
}
