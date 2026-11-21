// Components 64 and 66 (LOOK44, LOOK47): the Help page parts. Job path: the parts a job passes through, in order.
// Term help: the ? next to a word that opens a short answer and a link to the same card on the Help page. Pages pass
// data only (UX-2, UX-54).
import { useId, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Card } from './Card';
import { usePopoverDismiss } from './Popover';

export interface JobPathPart {
  id: string;
  /** The part's name, one or two words: Run. */
  title: string;
  /** One short line under the name: one start of a workflow. */
  line: string;
  /** The group frame the part sits in. */
  group: string;
  /** A live example of the part, from real data when there is some: octo/app. It stands in for `line` (LOOK48). */
  example?: string | undefined;
}

export interface JobPathGroup {
  id: string;
  label: string;
}

/**
 * Component 64. The parts of a job's way as numbered boxes in two framed groups (On GitHub, In your pool), left to
 * right on a wide screen and top to bottom on a phone. With `onSelect` each box is a button; a lit part has the
 * selection fill. Without it the picture is read only (the first-run Overview).
 */
export function JobPath({
  label,
  parts,
  groups,
  lit,
  onSelect,
}: {
  label: string;
  parts: readonly JobPathPart[];
  groups: readonly JobPathGroup[];
  lit?: readonly string[] | undefined;
  onSelect?: ((id: string) => void) | undefined;
}) {
  return (
    <ol className="ds-jobpath" aria-label={label} data-layout-group>
      {groups.map((g) => (
        <li key={g.id} className="ds-jobpath__group" data-group={g.id}>
          <span className="ds-jobpath__label">{g.label}</span>
          <ol className="ds-jobpath__parts">
            {parts
              .map((p, i) => ({ p, n: i + 1 }))
              .filter(({ p }) => p.group === g.id)
              .map(({ p, n }) => {
                const inner = (
                  <>
                    <span className="ds-jobpath__n" aria-hidden="true">
                      {n}
                    </span>
                    <span className="ds-jobpath__title">{p.title}</span>
                    {p.example === undefined ? (
                      <span className="ds-jobpath__line">{p.line}</span>
                    ) : (
                      <span className="ds-jobpath__line" data-example title={p.example}>
                        {p.example}
                      </span>
                    )}
                  </>
                );
                const on = lit?.includes(p.id) || undefined;
                return (
                  <li
                    key={p.id}
                    id={`box-${p.id}`}
                    className="ds-jobpath__part"
                    data-lit={on}
                  >
                    {onSelect ? (
                      <button
                        type="button"
                        className="ds-jobpath__box"
                        aria-pressed={lit ? Boolean(on) : undefined}
                        onClick={() => onSelect(p.id)}
                      >
                        {inner}
                      </button>
                    ) : (
                      <div className="ds-jobpath__box">{inner}</div>
                    )}
                  </li>
                );
              })}
          </ol>
        </li>
      ))}
    </ol>
  );
}

export const TERM_HELP_WORDS = { more: 'More about this in Help' } as const;

/**
 * Component 66. A ? button beside a term. It opens a short answer in a popover (a bottom sheet on a phone) with a
 * link to the same card on the Help page. Escape and a press outside close it (LOOK30).
 */
export function TermHelp({
  term,
  answer,
  to,
}: {
  /** The word it explains: Slots. The button is named `What does Slots mean?`. */
  term: string;
  /** One or two short sentences. */
  answer: ReactNode;
  /** The Help card of the term: `/help#q-slot`. */
  to: string;
}) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const { buttonRef, panelRef } = usePopoverDismiss(open, () => setOpen(false));
  return (
    <span className="ds-termhelp">
      <button
        ref={buttonRef}
        type="button"
        className="ds-termhelp__button"
        data-term-help
        aria-label={`What does ${term} mean?`}
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => setOpen((o) => !o)}
      >
        <span aria-hidden="true">?</span>
      </button>
      {open ? (
        <div
          ref={panelRef}
          id={id}
          className="ds-popover ds-termhelp__panel"
          role="group"
          aria-label={term}
        >
          <strong className="ds-strong">{term}</strong>
          <p className="ds-text">{answer}</p>
          <Link to={to} className="ds-link" onClick={() => setOpen(false)}>
            {TERM_HELP_WORDS.more}
          </Link>
        </div>
      ) : null}
    </span>
  );
}

/**
 * Component 69. A question card of the Help page: the card (component 8) with a badge that names the diagram box
 * the question is about (`Box 5: Slot`, with the numbered circle), then the answer and one small picture. The badge
 * is a link to the box in the job path (component 64) and the page lights that box. The page passes data only.
 */
export function HelpQuestionCard({
  id,
  title,
  box,
  onBox,
  picture,
  children,
}: {
  id: string;
  title: string;
  /** The box the card explains: its number in the job path and its name. */
  box: { id: string; n: number; name: string };
  /** Called when the badge is pressed; the page lights the box. */
  onBox: (id: string) => void;
  /** One small picture of component 69 (`PictureSlots` and the others), or nothing. */
  picture?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card id={id} title={title}>
      <a
        className="ds-boxbadge"
        href={`#box-${box.id}`}
        onClick={(e) => {
          e.preventDefault();
          onBox(box.id);
        }}
      >
        <span className="ds-boxbadge__n" aria-hidden="true">
          {box.n}
        </span>
        <span>{`Box ${box.n}: ${box.name}`}</span>
      </a>
      {children}
      {picture ? <div className="ds-helppic-frame">{picture}</div> : null}
    </Card>
  );
}
