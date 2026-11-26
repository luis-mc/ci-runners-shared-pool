// Component 78: the panel. A bordered box whose title is plain text set over the top border; a machine name is the one
// title that is bold and coloured. `tone` colours the border in the Terminal theme and nothing else (CRSP300). Pages
// pass data only; they never write a class or a style (UX-2).
import type { ReactNode } from 'react';

export type PanelTone = 'ok' | 'note' | 'needs' | 'problem' | 'accent' | 'quiet';

export function Panel({
  title,
  count,
  meta,
  tone = 'ok',
  machine = false,
  level = 2,
  children,
  footer,
  id,
}: {
  title: string;
  /** A number after the title, as in `machines · 2`. */
  count?: number | undefined;
  /** Dim text on the right of the top border, such as `live ●` or `3/5`. */
  meta?: string | undefined;
  /** The colour of the border (Terminal theme). The state is always also in words inside the panel (CORE-5). */
  tone?: PanelTone | undefined;
  /** The title is a machine name (HOME-PC): bold and in the tone colour. */
  machine?: boolean | undefined;
  level?: 2 | 3 | undefined;
  children: ReactNode;
  footer?: ReactNode | undefined;
  id?: string | undefined;
}) {
  const Heading = level === 2 ? 'h2' : 'h3';
  return (
    <section id={id} className="ds-panel" aria-label={title} data-tone={tone}>
      <Heading className="ds-panel__title" data-machine={machine || undefined}>
        {title}
        {count === undefined ? null : <span className="ds-panel__count"> · {count}</span>}
      </Heading>
      {meta ? <span className="ds-panel__meta">{meta}</span> : null}
      <div className="ds-panel__body">{children}</div>
      {footer ? <div className="ds-panel__footer">{footer}</div> : null}
    </section>
  );
}
