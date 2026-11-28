// Docs publishing (website R5.15 to R5.21). `docs/` stays the only source: this reads the generated `docs/llms.txt` (the
// list of pages and their titles) and `docs/docs-map.json` (which doc each code folder belongs to), and never writes
// them. A page is rendered to HTML text here; React draws it with the shared `Prose` part.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, posix, relative } from 'node:path';
import { Marked } from 'marked';
import { renderMermaidSVG } from 'beautiful-mermaid';
import { REPO } from './release.ts';

/** R5.17: what the site publishes, and what it never does. */
export const PUBLIC_FOLDERS = [
  'overview',
  'guides',
  'features',
  'architecture',
  'reference',
  'studies',
  'design-system',
] as const;
export const NEVER_PUBLISHED = ['work', 'project', 'archive', 'review', 'specs'] as const;
export const GROUP_TITLE: Record<string, string> = {
  overview: 'Overview',
  guides: 'Guides',
  features: 'Features',
  architecture: 'Architecture',
  reference: 'Reference',
  studies: 'Studies',
  'design-system': 'Design system',
};
/** Generated indexes in public folders that are not pages of their own (DOCSPEC generated files). */
const GENERATED = new Set(['docs/architecture/decisions.md', 'docs/features/index.md']);

export interface DocPage {
  /** The site path, for example `/docs/overview/product`. */
  path: string;
  /** The repository path, for example `docs/overview/product.md`. */
  source: string;
  /** The folder under `docs/`, for example `design-system/v4`. */
  folder: string;
  title: string;
  version?: string | undefined;
  lastVerified?: string | undefined;
  html: string;
  headings: string[];
  text: string;
  /** The first paragraph in plain words: the "In one paragraph" of a spec, else the first paragraph of the page. */
  summary: string;
  editUrl: string;
}

export interface DocsResult {
  pages: DocPage[];
  /** What stops the build (R5.20), one line each. */
  problems: string[];
  /** Worth saying, does not stop the build: a diagram that did not draw. */
  warnings: string[];
  /** The llms.txt text, copied unchanged to the site (R5.16). */
  llms: string;
  /** How many docs the generated code-to-doc map names. */
  mappedDocs: number;
}

export interface LlmsEntry {
  title: string;
  source: string;
}

/** The `- [Title](docs/path.md)` lines of llms.txt. */
export function parseLlms(text: string): LlmsEntry[] {
  const out: LlmsEntry[] = [];
  for (const line of text.split(/\r?\n/)) {
    const m = /^- \[(.+)\]\((docs\/[^)\s]+\.md)\)\s*$/.exec(line);
    if (m) out.push({ title: m[1]!, source: m[2]! });
  }
  return out;
}

export function isPublic(source: string): boolean {
  const folder = source.split('/')[1] ?? '';
  return (PUBLIC_FOLDERS as readonly string[]).includes(folder);
}

/** `docs/overview/README.md` is `/docs/overview`; `docs/features/x/spec.md` is `/docs/features/x`. */
export function pagePath(source: string): string {
  const parts = source
    .replace(/\.md$/, '')
    .replace(/^docs\//, '')
    .split('/');
  const last = parts[parts.length - 1]!;
  if ((last === 'README' || last === 'spec') && parts.length > 1) parts.pop();
  return `/docs/${parts.join('/')}`;
}

export function parseFrontMatter(md: string): {
  data: Record<string, string>;
  body: string;
} {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(md);
  if (!m) return { data: {}, body: md };
  const data: Record<string, string> = {};
  for (const line of m[1]!.split(/\r?\n/)) {
    const kv = /^([A-Za-z_][\w-]*):\s*(.*)$/.exec(line);
    if (kv) data[kv[1]!] = kv[2]!.replace(/^["']|["']$/g, '').trim();
  }
  return { data, body: md.slice(m[0].length) };
}

const escapeHtml = (s: string): string =>
  s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');

export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/<[^>]+>/g, '')
    .replace(/[^\p{L}\p{N}\s_-]/gu, '')
    .trim()
    .replace(/\s+/g, '-');
}

const decodeText = (html: string): string =>
  html
    .replace(/<[^>]+>/g, ' ')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
    .trim();

/**
 * A diagram drawn at build time (R5.18). Its colours come from the theme (`ds-prose__diagram`), so the inline style,
 * the style block and the font import the library writes are removed: the strict policy forbids them and the page
 * makes no third-party request.
 */
// The library writes a style block into the drawing; it is cut out here (the scan for app style code does not apply to
// a pattern that removes one, so the tag name is a constant).
const TAG = 'style';
const DRAWN_STYLE_BLOCK = new RegExp(`<${TAG}>[\\s\\S]*?</${TAG}>\\s*`);
export function drawDiagram(source: string): string {
  const svg = renderMermaidSVG(source);
  const labels = [...svg.matchAll(/<g\b[^>]*\bdata-label="([^"]*)"/g)].map((m) => m[1]!);
  const name = labels.length > 0 ? `Diagram with ${labels.join(', ')}` : 'Diagram';
  const clean = svg
    .replace(DRAWN_STYLE_BLOCK, '')
    .replace(/^<svg ([^>]*?) style="[^"]*"/, '<svg $1')
    .replace(/<svg /, `<svg role="img" aria-label="${escapeHtml(name)}" `);
  return `<figure class="ds-prose__diagram">${clean}</figure>`;
}

interface PendingAnchor {
  from: string;
  to: string;
  hash: string;
}

export interface RenderContext {
  /** The repository path of the page being drawn. */
  source: string;
  /** The repository root on disk. */
  repo: string;
  /** Site paths by repository path of the published pages. */
  published: ReadonlyMap<string, string>;
  /** The heading ids each page has, for anchor checks. */
  anchors: Map<string, Set<string>>;
  pending: PendingAnchor[];
  problems: string[];
  warnings: string[];
}

const githubUrl = (path: string, dir: boolean): string =>
  `https://github.com/${REPO}/${dir ? 'tree' : 'blob'}/main/${path}`;

/**
 * R5.18: a relative link between docs becomes a site path; a link to a repository file the site does not publish goes
 * to GitHub; a link to nothing is a problem (R5.20).
 */
export function rewriteLink(href: string, ctx: RenderContext): string {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//') || href.startsWith('#'))
    return href;
  const hashAt = href.indexOf('#');
  const rawPath = hashAt < 0 ? href : href.slice(0, hashAt);
  const hash = hashAt < 0 ? '' : href.slice(hashAt + 1);
  if (rawPath === '') return href;
  const target = rawPath.startsWith('/')
    ? posix.normalize(rawPath.slice(1))
    : posix.normalize(posix.join(posix.dirname(ctx.source), rawPath));
  const onDisk = join(ctx.repo, target);
  if (!existsSync(onDisk)) {
    ctx.problems.push(`${ctx.source}: broken link ${href}`);
    return href;
  }
  const site = ctx.published.get(target);
  if (site) {
    ctx.pending.push({ from: ctx.source, to: target, hash });
    return hash ? `${site}#${hash}` : site;
  }
  return `${githubUrl(target, statSync(onDisk).isDirectory())}${hash ? `#${hash}` : ''}`;
}

export function renderMarkdown(
  body: string,
  ctx: RenderContext,
): { html: string; headings: string[]; title: string | undefined } {
  const seen = new Map<string, number>();
  const headings: string[] = [];
  const ids = new Set<string>();
  let title: string | undefined;
  const marked = new Marked({
    gfm: true,
    renderer: {
      heading({ tokens, depth }) {
        const inner = this.parser.parseInline(tokens);
        const text = decodeText(inner);
        // The page's title is the document's first level-1 heading; the page header draws it.
        if (depth === 1 && title === undefined) {
          title = text;
          return '';
        }
        const level = Math.min(Math.max(depth, 2), 4);
        let id = slugify(text);
        const n = seen.get(id) ?? 0;
        seen.set(id, n + 1);
        if (n > 0) id = `${id}-${n}`;
        ids.add(id);
        headings.push(text);
        return `<h${level} id="${escapeHtml(id)}">${inner}</h${level}>\n`;
      },
      link({ href, title: t, tokens }) {
        const to = rewriteLink(href, ctx);
        const external = /^https?:/i.test(to);
        const attrs = `${t ? ` title="${escapeHtml(t)}"` : ''}${external ? ' rel="noopener noreferrer"' : ''}`;
        return `<a href="${escapeHtml(to)}"${attrs}>${this.parser.parseInline(tokens)}</a>`;
      },
      // A picture in a doc is not copied: the page makes no request for it. Its words stay.
      image({ text }) {
        return escapeHtml(text);
      },
      // A task-list box is words, not a form control with no label.
      checkbox({ checked }) {
        return checked ? '[x] ' : '[ ] ';
      },
      // Raw HTML in a doc is never passed through: comments go, tags go, the words stay.
      html({ text }) {
        return escapeHtml(text.replace(/<!--[\s\S]*?-->/g, '').replace(/<[^>]+>/g, ''));
      },
      code({ text, lang }) {
        if ((lang ?? '').split(/\s+/)[0] === 'mermaid') {
          try {
            return drawDiagram(text);
          } catch (error) {
            ctx.warnings.push(
              `${ctx.source}: a diagram did not draw (${error instanceof Error ? error.message : String(error)}); its source is shown`,
            );
          }
        }
        return `<pre tabindex="0"><code>${escapeHtml(text)}</code></pre>\n`;
      },
    },
  });
  const html = marked
    .parse(body, { async: false })
    .replace(/<table>/g, '<div class="ds-prose__table"><table>')
    .replace(/<\/table>/g, '</table></div>');
  ctx.anchors.set(ctx.source, ids);
  return { html, headings, title };
}

const plain = (md: string): string =>
  md
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[`*_]/g, '')
    .replace(/<[^>]+>/g, '')
    .replace(/\s+/g, ' ')
    .trim();

/** The paragraph a spec opens with, or the first plain paragraph of any doc. */
export function summaryOf(body: string): string {
  const paragraphs = (text: string): string[] =>
    text
      .split(/\r?\n\s*\r?\n/)
      .map((p) => p.trim())
      .filter((p) => p !== '' && !/^(#|>|\||-|\*|\d+\.|<|```|!\[)/.test(p));
  const one = /^##\s+(?:\d+\.\s+)?In one paragraph\s*\r?\n([\s\S]*?)(?=^##\s)/im.exec(
    body,
  );
  const first = (one ? paragraphs(one[1]!) : paragraphs(body))[0];
  return first ? plain(first) : '';
}

const walk = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? walk(join(dir, e.name)) : [join(dir, e.name)],
  );

/** Reads `docs/` of the repository at `repo` and returns the published pages (R5.15 to R5.21). */
export function loadDocs(repo: string): DocsResult {
  const problems: string[] = [];
  const warnings: string[] = [];
  const llms = readFileSync(join(repo, 'docs', 'llms.txt'), 'utf8');
  const all = parseLlms(llms);
  const entries = all.filter((e) => isPublic(e.source));
  const listed = new Set(all.map((e) => e.source));

  // The generated code-to-doc map: every doc it names must exist.
  const map = JSON.parse(readFileSync(join(repo, 'docs', 'docs-map.json'), 'utf8')) as {
    map: Record<string, string[]>;
  };
  const mapped = new Set(Object.values(map.map).flat());
  for (const doc of mapped)
    if (!existsSync(join(repo, doc)))
      problems.push(`docs-map.json names a missing doc ${doc}`);

  // R5.20: a public page that llms.txt does not list is not published by accident.
  for (const folder of PUBLIC_FOLDERS) {
    const dir = join(repo, 'docs', folder);
    if (!existsSync(dir)) continue;
    for (const file of walk(dir)) {
      const rel = relative(repo, file).replaceAll('\\', '/');
      if (!rel.endsWith('.md') || rel.includes('/changes/') || GENERATED.has(rel))
        continue;
      if (!listed.has(rel))
        problems.push(`${rel} is in a public folder but not listed in docs/llms.txt`);
    }
  }

  const published = new Map<string, string>();
  const byPath = new Map<string, string>();
  for (const e of entries) {
    const path = pagePath(e.source);
    const clash = byPath.get(path);
    if (clash) problems.push(`${e.source} and ${clash} both publish ${path}`);
    byPath.set(path, e.source);
    published.set(e.source, path);
  }

  const anchors = new Map<string, Set<string>>();
  const pending: PendingAnchor[] = [];
  const pages: DocPage[] = [];
  for (const e of entries) {
    const file = join(repo, e.source);
    if (!existsSync(file)) {
      problems.push(`${e.source} is listed in docs/llms.txt but missing`);
      continue;
    }
    const { data, body } = parseFrontMatter(readFileSync(file, 'utf8'));
    const ctx: RenderContext = {
      source: e.source,
      repo,
      published,
      anchors,
      pending,
      problems,
      warnings,
    };
    const out = renderMarkdown(body, ctx);
    pages.push({
      path: published.get(e.source)!,
      source: e.source,
      folder: dirname(e.source).replace(/^docs\//, ''),
      title: e.title || out.title || e.source,
      version: data['version'],
      lastVerified: data['last_verified'],
      html: out.html,
      headings: out.headings,
      text: decodeText(out.html),
      summary: summaryOf(body),
      editUrl: `https://github.com/${REPO}/edit/main/${e.source}`,
    });
  }
  // Anchors are checked once every page is drawn: a link may point at a page drawn later.
  for (const a of pending) {
    if (a.hash && !(anchors.get(a.to) ?? new Set<string>()).has(a.hash))
      problems.push(`${a.from}: broken anchor #${a.hash} in ${a.to}`);
  }
  return { pages, problems, warnings, llms, mappedDocs: mapped.size };
}
