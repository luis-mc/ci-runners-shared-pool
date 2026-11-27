// Keyboard first (UX-57, R5.5). Ctrl/Cmd+K opens the palette; `/` goes to the search bar; Enter opens the row;
// `?` lists every shortcut. Down, Up, j and k move focus through the page: that is `ArrowNavigation` in @crsp/ui.
// None of these fires while typing in a field.
export const SHORTCUTS: readonly { keys: string; does: string }[] = [
  { keys: 'Ctrl+K', does: 'Open the command palette (Cmd+K on macOS)' },
  { keys: '/', does: "Go to the page's search bar" },
  { keys: 'Down or j', does: 'Move to the next control or list item' },
  { keys: 'Up or k', does: 'Move to the previous control or list item' },
  { keys: 'Enter', does: 'Open the chosen row' },
  { keys: '?', does: 'List every shortcut' },
  { keys: 'Escape', does: 'Close a dialog' },
];

/** The key-hint bar at the page foot (UX-57). */
export const KEY_HINTS: readonly { keys: string; does: string }[] = [
  { keys: '/', does: 'search' },
  { keys: 'Enter', does: 'open' },
  { keys: '?', does: 'keys' },
];

export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return (
    tag === 'INPUT' ||
    tag === 'TEXTAREA' ||
    tag === 'SELECT' ||
    target.isContentEditable ||
    target.getAttribute('role') === 'textbox'
  );
}

export interface ShortcutHandlers {
  openPalette: () => void;
  openHelp: () => void;
}

/** Handle one key event. Returns true when it was a shortcut. */
export function handleKey(
  e: KeyboardEvent,
  h: ShortcutHandlers,
  root: ParentNode = document,
): boolean {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    h.openPalette();
    return true;
  }
  if (e.ctrlKey || e.metaKey || e.altKey) return false;
  if (isTyping(e.target)) return false;
  if (e.key === '?') {
    e.preventDefault();
    h.openHelp();
    return true;
  }
  if (e.key === '/') {
    const bar = root.querySelector<HTMLElement>('[data-search]');
    if (bar) {
      e.preventDefault();
      bar.focus();
      return true;
    }
    return false;
  }
  if (e.key === 'Enter') {
    const row = document.activeElement;
    if (row instanceof HTMLElement && row.hasAttribute('data-list-row')) {
      const link = row.querySelector<HTMLElement>('a');
      if (link) {
        link.click();
        return true;
      }
    }
  }
  return false;
}

export function isMac(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);
}
