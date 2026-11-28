// Components 86 to 91: the marketing frame of a public, signed-out site (website R4): the frame with its page gutter, the
// hero, the feature band, a screenshot, running text from the docs, and a static doc search. No script is needed to
// read any of them; the search box only gains a behaviour when the site's one script finds it.
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

/** Component 86: the frame of a public page. The menu, the page and the footer share one gutter and one width. */
export function SiteFrame({
  menu,
  children,
  footer,
}: {
  menu: ReactNode;
  children: ReactNode;
  footer: ReactNode;
}) {
  return (
    <div className="ds-site">
      <a className="ds-site__skip" href="#main">
        Skip to the content
      </a>
      <header className="ds-site__bar">{menu}</header>
      <main id="main" className="ds-site__main" data-layout-group>
        {children}
      </main>
      <footer className="ds-site__footer">{footer}</footer>
    </div>
  );
}

/** Component 87: the top of a landing page: the one headline, one line under it, an optional action, and one picture. */
export function Hero({
  title,
  lead,
  action,
  media,
}: {
  title: string;
  lead: string;
  /** The page's one primary action (UX-3). */
  action?: ReactNode;
  media?: ReactNode | undefined;
}) {
  return (
    <section className="ds-hero" aria-label={title} data-layout-group>
      <div className="ds-hero__text">
        <h1 className="ds-h1 ds-hero__title">{title}</h1>
        <p className="ds-hero__lead">{lead}</p>
        {action ? <div className="ds-hero__action">{action}</div> : null}
      </div>
      {media ? <div className="ds-hero__media">{media}</div> : null}
    </section>
  );
}

/** Component 88: one band of a landing page: a level-2 heading, an optional line, the content, and an optional picture beside it on a wide screen. */
export function FeatureBand({
  title,
  lead,
  media,
  children,
}: {
  title: string;
  lead?: string | undefined;
  media?: ReactNode | undefined;
  children: ReactNode;
}) {
  return (
    <section
      className="ds-band-feature"
      aria-label={title}
      data-media={media ? true : undefined}
      data-layout-group
    >
      <div className="ds-band-feature__text">
        <h2 className="ds-h2">{title}</h2>
        {lead ? <p className="ds-band-feature__lead">{lead}</p> : null}
        <div className="ds-band-feature__body">{children}</div>
      </div>
      {media ? <div className="ds-band-feature__media">{media}</div> : null}
    </section>
  );
}

/** Component 89: a screenshot with its words for people who cannot see it. The size is stated, so the page does not jump while it loads. */
export function Shot({
  src,
  alt,
  width,
  height,
  caption,
  priority = false,
}: {
  src: string;
  /** What the picture shows, in words (CORE-5). */
  alt: string;
  width: number;
  height: number;
  caption?: string | undefined;
  /** The first picture on the page loads at once; the others load when they come near (R6.1). */
  priority?: boolean | undefined;
}) {
  return (
    <figure className="ds-shot">
      <img
        className="ds-shot__img"
        src={src}
        alt={alt}
        width={width}
        height={height}
        loading={priority ? 'eager' : 'lazy'}
        decoding="async"
      />
      {caption ? <figcaption className="ds-shot__caption">{caption}</figcaption> : null}
    </figure>
  );
}

/**
 * Component 90: running text written elsewhere (a doc), drawn with the design system's type: headings, paragraphs,
 * lists, tables, code and links. `html` is the rendered body; the caller escapes any raw HTML in the source first.
 */
export function Prose({ html }: { html: string }) {
  return <div className="ds-prose" dangerouslySetInnerHTML={{ __html: html }} />;
}

export interface DocSearchGroup {
  title: string;
  items: readonly { to: string; title: string; hint?: string | undefined }[];
}

/**
 * Component 91: a search box over a static list of pages. It draws the box, one status line and the list, grouped.
 * The site's script reads `data-doc-search`, `data-doc-path` and `data-doc-group` and hides what does not match.
 * Without script the box is hidden and the full list stays.
 */
export function DocSearch({
  label,
  hint,
  groups,
}: {
  label: string;
  hint: string;
  groups: readonly DocSearchGroup[];
}) {
  return (
    <div className="ds-docsearch" data-doc-search>
      <div className="ds-search ds-docsearch__box" role="search" hidden data-layout-group>
        <input
          className="ds-input"
          type="search"
          aria-label={label}
          placeholder={hint}
          autoComplete="off"
          data-doc-search-input
        />
      </div>
      <p className="ds-text" data-tone="muted" role="status" data-doc-search-status />
      <div className="ds-docsearch__list">
        {groups.map((g) => (
          <section
            key={g.title}
            className="ds-docsearch__group"
            aria-label={g.title}
            data-doc-group
          >
            <h2 className="ds-h2">{g.title}</h2>
            <ul className="ds-docsearch__items">
              {g.items.map((it) => (
                <li key={it.to} data-doc-path={it.to}>
                  <Link to={it.to} className="ds-link">
                    {it.title}
                  </Link>
                  {it.hint ? (
                    <span className="ds-docsearch__hint"> {it.hint}</span>
                  ) : null}
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </div>
  );
}
