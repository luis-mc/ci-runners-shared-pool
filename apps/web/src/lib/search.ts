// Docs search (website R5.19): a static index made at build time and searched in the browser. No search service.
// The index maps each word to the pages that hold it; words in a title or a heading are kept apart so they rank first.
// The same functions run in the build (to write the index) and in the page's script (to search it).

export interface SearchIndex {
  pages: { path: string; title: string }[];
  /** Word to page numbers: any word of the page. */
  words: Record<string, number[]>;
  /** Word to page numbers: a word of the title or of a heading. */
  heads: Record<string, number[]>;
}

export interface Indexable {
  path: string;
  title: string;
  headings: readonly string[];
  text: string;
}

export function tokenize(text: string): string[] {
  return [...new Set(text.toLowerCase().match(/[\p{L}\p{N}]{2,}/gu) ?? [])];
}

const add = (into: Record<string, number[]>, word: string, page: number): void => {
  if (!Object.hasOwn(into, word)) into[word] = [];
  into[word]!.push(page);
};

export function buildIndex(docs: readonly Indexable[]): SearchIndex {
  const index: SearchIndex = { pages: [], words: {}, heads: {} };
  docs.forEach((d, i) => {
    index.pages.push({ path: d.path, title: d.title });
    for (const w of tokenize(d.text)) add(index.words, w, i);
    for (const w of tokenize([d.title, ...d.headings].join(' '))) add(index.heads, w, i);
  });
  return index;
}

const pagesWith = (table: Record<string, number[]>, prefix: string): Set<number> => {
  const out = new Set<number>();
  for (const [word, pages] of Object.entries(table))
    if (word.startsWith(prefix)) for (const p of pages) out.add(p);
  return out;
};

/**
 * The paths that match every word of the query (a word matches by its start), best first: a page whose title or
 * headings hold the words ranks above one that holds them in the text.
 */
export function search(index: SearchIndex, query: string): string[] {
  const words = tokenize(query);
  if (words.length === 0) return [];
  const scores = new Map<number, number>();
  let first = true;
  for (const w of words) {
    const inText = pagesWith(index.words, w);
    const inHeads = pagesWith(index.heads, w);
    const hit = new Set([...inText, ...inHeads]);
    for (const p of first ? hit : [...scores.keys()]) {
      if (!hit.has(p)) {
        scores.delete(p);
        continue;
      }
      scores.set(p, (scores.get(p) ?? 0) + (inHeads.has(p) ? 3 : 1));
    }
    first = false;
  }
  return [...scores.entries()]
    .sort((a, b) => b[1] - a[1] || a[0] - b[0])
    .map(([p]) => index.pages[p]!.path);
}
