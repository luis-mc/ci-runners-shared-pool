// R5.5: the shortcuts work as the spec says, and none fires while typing in a field (UX-57).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { handleKey, isTyping } from './shortcuts';

afterEach(() => {
  document.body.innerHTML = '';
});

function key(
  k: string,
  init: KeyboardEventInit = {},
  target: Element = document.body,
): KeyboardEvent {
  const e = new KeyboardEvent('keydown', {
    key: k,
    bubbles: true,
    cancelable: true,
    ...init,
  });
  Object.defineProperty(e, 'target', { value: target });
  return e;
}

describe('shortcuts (R5.5)', () => {
  const h = () => ({ openPalette: vi.fn(), openHelp: vi.fn() });

  it('Ctrl+K and Cmd+K open the palette, even while typing', () => {
    document.body.innerHTML = '<input id="f" />';
    const f = document.getElementById('f') as HTMLElement;
    const handlers = h();
    expect(handleKey(key('k', { ctrlKey: true }, f), handlers)).toBe(true);
    expect(handleKey(key('k', { metaKey: true }), handlers)).toBe(true);
    expect(handlers.openPalette).toHaveBeenCalledTimes(2);
  });

  it('? opens the shortcut list', () => {
    const handlers = h();
    expect(handleKey(key('?'), handlers)).toBe(true);
    expect(handlers.openHelp).toHaveBeenCalled();
  });

  it('/ moves to the search bar', () => {
    document.body.innerHTML = '<input data-search id="s" />';
    expect(handleKey(key('/'), h())).toBe(true);
    expect(document.activeElement?.id).toBe('s');
  });

  it('Enter opens the row through its link', () => {
    document.body.innerHTML =
      '<table><tbody><tr data-list-row tabindex="0" id="a"><td><a href="#x" id="l">open</a></td></tr></tbody></table>';
    (document.getElementById('a') as HTMLElement).focus();
    const click = vi.fn();
    (document.getElementById('l') as HTMLElement).addEventListener('click', click);
    handleKey(key('Enter'), h());
    expect(click).toHaveBeenCalled();
  });

  it('none fires while typing in a field', () => {
    document.body.innerHTML =
      '<input id="f" /><textarea id="t"></textarea><input data-search />';
    const handlers = h();
    for (const id of ['f', 't']) {
      const el = document.getElementById(id) as HTMLElement;
      expect(isTyping(el)).toBe(true);
      expect(handleKey(key('?', {}, el), handlers)).toBe(false);
      expect(handleKey(key('/', {}, el), handlers)).toBe(false);
      expect(handleKey(key('j', {}, el), handlers)).toBe(false);
    }
    expect(handlers.openHelp).not.toHaveBeenCalled();
  });
});
