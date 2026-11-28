// Component 44: the action bar (UX-71). The main action of a screen sits in one bar at the bottom of the
// window, inside the main content and above the key-hint bar. It is always visible: it never scrolls away.
// Order: what the bar says (the selection count, or the unsaved changes) on the left, the secondary actions
// (Cancel, Back) next, the one primary action on the right. On a phone the bar is full width and the buttons
// stack at equal width with the primary on top. A screen draws at most one bar and one primary (UX-4).
import {
  createContext,
  useContext,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';

/**
 * The place where the bar is drawn (RES21 T1). `Frame` provides it right after the main column, as a child of the
 * frame: the bar sticks to the window and not to a wrapper that ends above the page foot, and a tall header or
 * banner above the main column never holds it over the key-hint bar (CRSP381).
 * `slot` is null until the frame has mounted. Outside a frame there is no value and the bar draws in place.
 */
export const ActionBarSlotContext = createContext<{ slot: HTMLElement | null } | null>(
  null,
);

/**
 * The form a bar was written inside. The bar is drawn elsewhere in the page (its slot), so a submit button in it
 * names its form with the `form` attribute: Enter in a field and the button still send that form.
 */
export const FormOwnerContext = createContext<string | undefined>(undefined);

export interface ActionBarProps {
  /** What the bar says about the work: "2 selected", "Unsaved changes (3)". */
  status?: ReactNode | undefined;
  /** Secondary actions (Cancel, Back, Stop). They sit to the left of the primary action. */
  secondary?: ReactNode | undefined;
  /** The one primary action of the screen. */
  primary: ReactNode;
  /** The name of the bar for a screen reader. */
  label?: string | undefined;
  /** False for a bar inside a screen that already has its own bar (an embedded wizard): it then sits in the flow. */
  sticky?: boolean | undefined;
}

export function ActionBar({
  status,
  secondary,
  primary,
  label = 'Actions',
  sticky = true,
}: ActionBarProps) {
  const host = useContext(ActionBarSlotContext);
  const anchor = useRef<HTMLSpanElement>(null);
  const fallbackId = useId();
  const [formId, setFormId] = useState<string | undefined>(undefined);
  useLayoutEffect(() => {
    const form = anchor.current?.closest('form');
    if (!form) return;
    if (!form.id) form.id = fallbackId;
    setFormId(form.id);
  }, [fallbackId]);
  const bar = (
    <div
      className="ds-action-bar"
      role="region"
      aria-label={label}
      data-sticky={sticky}
      data-layout-group
    >
      {status ? (
        <p className="ds-action-bar__status" aria-live="polite">
          {status}
        </p>
      ) : null}
      {secondary ? (
        <div className="ds-action-bar__side" data-layout-group>
          {secondary}
        </div>
      ) : null}
      <div className="ds-action-bar__main" data-layout-group>
        {primary}
      </div>
    </div>
  );
  if (!host || !sticky) return bar;
  return (
    <>
      <span ref={anchor} hidden />
      {host.slot
        ? createPortal(
            <FormOwnerContext.Provider value={formId}>{bar}</FormOwnerContext.Provider>,
            host.slot,
          )
        : null}
    </>
  );
}

/** "2 selected" for a selection screen. */
export function selectedStatus(count: number): string {
  return `${count} selected`;
}

/** "Unsaved changes (3)" for an edit screen; nothing when clean. */
export function unsavedStatus(count: number): string | undefined {
  return count > 0 ? `Unsaved changes (${count})` : undefined;
}
