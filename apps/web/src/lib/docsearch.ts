// The docs search box in the browser (website R5.19). The page carries every doc in a list; this reads the index once, on
// the first key, and hides the rows that do not match. Without script the box stays hidden and the whole list shows.
import { search, type SearchIndex } from './search.ts';

export const INDEX_URL = '/search-index.json';

export function attachDocSearch(root: ParentNode = document): void {
  const box = root.querySelector<HTMLElement>('[data-doc-search]');
  if (!box) return;
  const input = box.querySelector<HTMLInputElement>('[data-doc-search-input]');
  const status = box.querySelector<HTMLElement>('[data-doc-search-status]');
  const field = input?.closest<HTMLElement>('[role="search"]');
  if (!input || !status || !field) return;
  const rows = [...box.querySelectorAll<HTMLElement>('[data-doc-path]')];
  const groups = [...box.querySelectorAll<HTMLElement>('[data-doc-group]')];
  let index: Promise<SearchIndex> | undefined;

  const show = (paths: ReadonlySet<string> | null): void => {
    for (const row of rows)
      row.hidden = paths !== null && !paths.has(row.dataset['docPath'] ?? '');
    for (const g of groups)
      g.hidden = [...g.querySelectorAll<HTMLElement>('[data-doc-path]')].every(
        (r) => r.hidden,
      );
  };

  const run = async (): Promise<void> => {
    const query = input.value;
    if (query.trim() === '') {
      show(null);
      status.textContent = '';
      return;
    }
    index ??= fetch(INDEX_URL).then((r) => r.json() as Promise<SearchIndex>);
    let found: string[];
    try {
      found = search(await index, query);
    } catch {
      index = undefined;
      status.textContent = 'Search is not available. The full list is below.';
      show(null);
      return;
    }
    if (input.value !== query) return;
    show(new Set(found));
    status.textContent =
      found.length === 0
        ? 'No page matches. Clear the search to see every page.'
        : `${found.length} ${found.length === 1 ? 'page matches' : 'pages match'}`;
  };

  field.hidden = false;
  input.addEventListener('input', () => void run());
  // Warm the index when the box is first used, so the first key already finds pages.
  input.addEventListener('focus', () => {
    index ??= fetch(INDEX_URL).then((r) => r.json() as Promise<SearchIndex>);
    index.catch(() => (index = undefined));
  });
}
