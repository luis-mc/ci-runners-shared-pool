// The frame of every signed-in screen (web-console spec 4.0): header, top menu (component 2), banner slot,
// activity strip (component 27) and the page. Landmarks: header, nav, main (UX-17).
import { useMemo, useState, type ReactNode } from 'react';
import { ActionBarSlotContext } from './ActionBar';
import { Link, NavLink } from 'react-router-dom';
import { Button } from './Button';
import { KeyHintsProvider } from './KeyHints';
import { formatTime } from './format';
import { usePopoverDismiss } from './Popover';
import { Progress } from './Progress';
import { Status } from './Status';

export interface NavItem {
  to: string;
  label: string;
  end?: boolean | undefined;
}

export function TopMenu({
  items,
  label = 'Areas',
}: {
  items: readonly NavItem[];
  label?: string;
}) {
  return (
    <nav className="ds-topmenu" aria-label={label}>
      <ul className="ds-topmenu__list ds-band" data-frame-band>
        {items.map((i) => (
          <li key={i.to}>
            <NavLink
              to={i.to}
              {...(i.end ? { end: true } : {})}
              className={() => 'ds-topmenu__link'}
            >
              {i.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}

/** The phone's one Menu button, on the frame edge like the top menu it opens (UX-69, UX-15). */
export function TopMenuButton({
  open,
  here,
  onToggle,
}: {
  open: boolean;
  /** The label of the open area. */
  here: string;
  onToggle: () => void;
}) {
  return (
    <div className="ds-topmenu__toggle ds-band" data-frame-band data-menu-toggle>
      <Button onPress={onToggle}>{open ? 'Close the menu' : `Menu: ${here}`}</Button>
    </div>
  );
}

export function Breadcrumbs({
  trail,
}: {
  trail: readonly { to?: string; label: string }[];
}) {
  return (
    <nav className="ds-breadcrumbs" aria-label="You are here">
      <ol className="ds-breadcrumbs__list">
        {trail.map((t, i) => (
          <li key={`${t.label}-${i}`}>
            {t.to && i < trail.length - 1 ? (
              <Link to={t.to} className="ds-link">
                {t.label}
              </Link>
            ) : (
              <span aria-current={i === trail.length - 1 ? 'page' : undefined}>
                {t.label}
              </span>
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}

export interface GitHubSignInState {
  state: 'active' | 'ended';
  /** When an active sign-in ends (an ISO time), if known. */
  expiresAt?: string | undefined;
}

/** The account button and its menu: who you are, your GitHub sign-in, sign-in again, sign out (LOOK15). */
export function UserMenu({
  login,
  role,
  onSignOut,
  busy,
  githubSignIn,
  onSignInAgain,
  signInAgainBusy = false,
}: {
  login: string;
  role: string;
  onSignOut: () => void;
  busy: boolean;
  githubSignIn?: GitHubSignInState | undefined;
  onSignInAgain?: (() => void) | undefined;
  signInAgainBusy?: boolean | undefined;
}) {
  const [open, setOpen] = useState(false);
  const { buttonRef, panelRef } = usePopoverDismiss(open, () => setOpen(false));
  const ended = githubSignIn?.state === 'ended';
  return (
    <div className="ds-usermenu">
      <button
        ref={buttonRef}
        type="button"
        className="ds-button"
        aria-expanded={open}
        aria-controls="ds-usermenu-panel"
        onClick={() => setOpen((o) => !o)}
      >
        {login}
        {ended ? (
          <span
            className="ds-usermenu__dot"
            role="img"
            aria-label="GitHub sign-in ended"
          />
        ) : null}
      </button>
      {open ? (
        <div
          ref={panelRef}
          id="ds-usermenu-panel"
          className="ds-popover"
          role="group"
          aria-label="Account"
        >
          <p className="ds-text">
            Signed in as <strong className="ds-strong">{login}</strong>, role {role}.
          </p>
          {githubSignIn ? (
            ended ? (
              <Status kind="waiting" word="GitHub sign-in: ended" />
            ) : (
              <p className="ds-text">
                {githubSignIn.expiresAt
                  ? `GitHub sign-in: active until ${formatTime(githubSignIn.expiresAt)}`
                  : 'GitHub sign-in: active'}
              </p>
            )
          ) : null}
          {ended && onSignInAgain ? (
            <Button
              onPress={onSignInAgain}
              busy={signInAgainBusy}
              busyLabel="Opening GitHub"
            >
              Sign in with GitHub again
            </Button>
          ) : null}
          <Button onPress={onSignOut} busy={busy} busyLabel="Signing out">
            Sign out
          </Button>
        </div>
      ) : null}
    </div>
  );
}

export function Header({
  product,
  status,
  live,
  paletteHint,
  onPalette,
  menu,
}: {
  product: string;
  status?: ReactNode | undefined;
  live?: ReactNode | undefined;
  paletteHint: string;
  onPalette: () => void;
  menu?: ReactNode | undefined;
}) {
  return (
    <header className="ds-header">
      <div className="ds-header__frame ds-band" data-frame-band>
        <div className="ds-header__group" data-layout-group>
          <span className="ds-header__product">{product}</span>
          <span className="ds-header__state">
            <span className="ds-header__status">{status}</span>
            <span className="ds-header__live">{live}</span>
          </span>
        </div>
        <div className="ds-header__group" data-layout-group>
          <button
            type="button"
            className="ds-button ds-header__commands"
            onClick={onPalette}
            aria-keyshortcuts="Control+K Meta+K"
          >
            Commands <kbd className="ds-kbd">{paletteHint}</kbd>
          </button>
          <span className="ds-header__login">{menu}</span>
        </div>
      </div>
    </header>
  );
}

export interface ActivityJob {
  id: string;
  label: string;
  value?: number | undefined;
  max?: number | undefined;
  left?: string | undefined;
}

/** Component 27: running work in one strip. Closed it is one line with the lead job and a bar. */
export function ActivityStrip({ jobs }: { jobs: readonly ActivityJob[] }) {
  const [open, setOpen] = useState(false);
  const lead = jobs[0];
  if (!lead) return null;
  return (
    <section className="ds-strip" aria-label="Running work" data-layout-group>
      <button
        type="button"
        className="ds-button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        {open ? 'Hide' : 'Show'}
      </button>
      <span className="ds-strip__lead">
        {lead.label}
        {lead.left ? `, ${lead.left}` : ''}
      </span>
      <Progress label={lead.label} value={lead.value} max={lead.max} />
      {open ? (
        <ul className="ds-list">
          {jobs.map((j) => (
            <li key={j.id}>{j.label}</li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

export function Frame({
  header,
  menu,
  banner,
  strip,
  hints,
  children,
}: {
  header: ReactNode;
  menu: ReactNode;
  banner?: ReactNode | undefined;
  strip?: ReactNode | undefined;
  /** The key-hint bar at the window foot (`KeyHints`). */
  hints?: ReactNode | undefined;
  children: ReactNode;
}) {
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  const host = useMemo(() => ({ slot }), [slot]);
  return (
    <KeyHintsProvider>
      <ActionBarSlotContext.Provider value={host}>
        <div className="ds-frame">
          {header}
          {menu}
          {banner}
          {strip}
          <main className="ds-main ds-band" id="main" data-frame-band>
            {children}
          </main>
          {/* The action bar is drawn here, right after the main column and as a child of the frame, so it sticks
              against the whole frame: a tall header, menu or banner above the main column never holds it below
              its place over the key-hint bar at the top of the page (UX-71). */}
          <div className="ds-bar-slot" ref={setSlot} />
          {hints}
        </div>
      </ActionBarSlotContext.Provider>
    </KeyHintsProvider>
  );
}

/** A table of shortcuts, for the `?` overlay. */
export function ShortcutTable({
  items,
}: {
  items: readonly { keys: string; does: string }[];
}) {
  return (
    <dl className="ds-facts">
      {items.map((s) => (
        <div className="ds-facts__row" key={s.keys}>
          <dt>
            <kbd className="ds-kbd">{s.keys}</kbd>
          </dt>
          <dd>{s.does}</dd>
        </div>
      ))}
    </dl>
  );
}

/** One row of a result list (palette): a button that goes or runs. */
export function ResultList({
  label,
  items,
  active,
  onPick,
}: {
  label: string;
  items: readonly { id: string; label: string; hint?: string }[];
  active: number;
  onPick: (id: string) => void;
}) {
  return (
    <ul className="ds-results" role="listbox" aria-label={label}>
      {items.map((it, i) => (
        <li key={it.id} role="option" aria-selected={i === active}>
          <button
            type="button"
            className="ds-results__item"
            data-active={i === active || undefined}
            tabIndex={-1}
            onClick={() => onPick(it.id)}
          >
            {it.label}
            {it.hint ? <span className="ds-results__hint"> {it.hint}</span> : null}
          </button>
        </li>
      ))}
    </ul>
  );
}
