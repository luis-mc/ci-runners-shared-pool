// Component 41: the key-hint bar (UX-57). It sits fixed at the window foot and is not drawn on a touch or coarse
// pointer. It shows only the keys that work on the page: a page registers its keys with `useKeyHints`, and a part
// that adds a key (the table adds j, k and Enter) registers it too, so `j / k` shows only where a list exists.
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';

export interface KeyHint {
  keys: string;
  does: string;
}

interface Registry {
  set: (owner: symbol, items: readonly KeyHint[]) => void;
  clear: (owner: symbol) => void;
  all: readonly KeyHint[];
}

/** One key is one entry: `j / k`, `j k` and `J/K` are the same key. */
function keyId(keys: string): string {
  return keys.toLowerCase().replace(/[\s/,]+/g, '');
}

const Ctx = createContext<Registry | null>(null);

/** Holds the keys the mounted parts registered. `Frame` provides one; a test may wrap its own. */
export function KeyHintsProvider({ children }: { children: ReactNode }) {
  const [owners, setOwners] = useState<ReadonlyMap<symbol, readonly KeyHint[]>>(
    new Map(),
  );
  const registry = useMemo<Registry>(() => {
    const merged: KeyHint[] = [];
    const seen = new Set<string>();
    for (const items of owners.values())
      for (const h of items)
        if (!seen.has(keyId(h.keys))) {
          seen.add(keyId(h.keys));
          merged.push(h);
        }
    return {
      all: merged,
      set: (owner, items) =>
        setOwners((m) => {
          const prev = m.get(owner);
          if (
            prev &&
            prev.length === items.length &&
            prev.every((h, i) => h.keys === items[i]?.keys && h.does === items[i]?.does)
          )
            return m;
          return new Map(m).set(owner, items);
        }),
      clear: (owner) =>
        setOwners((m) => {
          if (!m.has(owner)) return m;
          const next = new Map(m);
          next.delete(owner);
          return next;
        }),
    };
  }, [owners]);
  return <Ctx.Provider value={registry}>{children}</Ctx.Provider>;
}

/** Registers the keys a page or part handles while it is mounted. An empty list registers nothing. */
export function useKeyHints(items: readonly KeyHint[]): void {
  const registry = useContext(Ctx);
  const owner = useRef(Symbol('key-hints'));
  const signature = JSON.stringify(items);
  useEffect(() => {
    if (!registry) return;
    const id = owner.current;
    registry.set(id, JSON.parse(signature) as KeyHint[]);
    return () => registry.clear(id);
    // The registry object changes with every registration; only the keys decide.
  }, [signature, registry === null]);
}

/**
 * Writes the real height of the bar (it wraps onto two lines on a phone) to `--ds-keyhints-height` on the frame, so
 * the action bar and the page foot reserve what the bar takes, not a fixed 44 px. A bar that is not drawn gives 0.
 */
function measureHeight(el: HTMLElement | null): (() => void) | void {
  const frame = el?.closest<HTMLElement>('.ds-frame');
  if (!el || !frame || typeof ResizeObserver === 'undefined') return;
  let live = true;
  const write = () => {
    if (live)
      frame.style.setProperty(
        '--ds-keyhints-height',
        `${el.getBoundingClientRect().height}px`,
      );
  };
  write();
  // The observer reports a size change after the frame's layout. A change that lands between frames (a window
  // resize, a font that finishes loading) is read at once by anything that forces layout, so the same writes also
  // run on `resize` and when fonts load: the offset the action bar keeps never lags the bar by a frame (UX-71).
  const ro = new ResizeObserver(write);
  ro.observe(el, { box: 'border-box' });
  window.addEventListener('resize', write);
  const fonts = document.fonts;
  fonts?.addEventListener?.('loadingdone', write);
  void fonts?.ready?.then(write);
  return () => {
    live = false;
    ro.disconnect();
    window.removeEventListener('resize', write);
    fonts?.removeEventListener?.('loadingdone', write);
    frame.style.removeProperty('--ds-keyhints-height');
  };
}

/** The bar: the registered keys, plus any `items` the caller passes. Draws nothing when no key works. */
export function KeyHints({ items = [] }: { items?: readonly KeyHint[] | undefined }) {
  const registry = useContext(Ctx);
  const merged: KeyHint[] = [];
  const seen = new Set<string>();
  for (const h of [...items, ...(registry?.all ?? [])])
    if (!seen.has(keyId(h.keys))) {
      seen.add(keyId(h.keys));
      merged.push(h);
    }
  if (merged.length === 0) return null;
  return (
    <footer className="ds-keyhints" aria-label="Keys" ref={measureHeight}>
      <ul className="ds-keyhints__list ds-band" data-frame-band>
        {merged.map((h) => (
          <li key={keyId(h.keys)}>
            <kbd className="ds-kbd">{h.keys}</kbd> {h.does}
          </li>
        ))}
      </ul>
    </footer>
  );
}
