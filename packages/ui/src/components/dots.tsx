// The dotted form of the data charts (UX-91, CRSP370): in the Cool Terminal themes with Graphs set to Dots, a chart is
// drawn with solid round filled dots. A stretched SVG would squash a circle, so the dots are drawn here, in an overlay
// whose circles sit at a percent of the plot and keep a fixed pixel radius. The pages never draw a dot.
import { useEffect, useState, type ReactNode, type RefObject } from 'react';

/** The radius of a dot, in pixels, and the gap between two dots. */
export const DOT_R = 3;
export const DOT_GAP = 10;

const on = (): boolean =>
  typeof document !== 'undefined' &&
  document.documentElement.getAttribute('data-theme-name') === 'terminal' &&
  document.documentElement.getAttribute('data-graphs') === 'dotted';

/** True while the page root says Cool Terminal with Graphs set to Dots; follows the setting when it changes. */
export function useDotted(): boolean {
  const [dotted, setDotted] = useState(on);
  useEffect(() => {
    setDotted(on());
    const mo = new MutationObserver(() => setDotted(on()));
    mo.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-graphs', 'data-theme-name'],
    });
    return () => mo.disconnect();
  }, []);
  return dotted;
}

/** The size in pixels of an element (600 by 200 before it is measured, and where nothing can measure). */
export function useBox(ref: RefObject<HTMLElement | null>, active: boolean) {
  const [box, setBox] = useState({ w: 600, h: 200 });
  useEffect(() => {
    const el = ref.current;
    if (!active || !el) return undefined;
    const read = () => {
      const r = el.getBoundingClientRect();
      if (r.width > 0 && r.height > 0) setBox({ w: r.width, h: r.height });
    };
    read();
    if (typeof ResizeObserver === 'undefined') return undefined;
    const ro = new ResizeObserver(read);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref, active]);
  return box;
}

export interface Px {
  x: number;
  y: number;
}

/** Points every `gap` pixels along a path of pixel points. */
export function along(path: readonly Px[], gap: number = DOT_GAP): Px[] {
  const out: Px[] = [];
  const first = path[0];
  if (!first) return out;
  out.push(first);
  let carry = 0;
  for (let i = 1; i < path.length && out.length < 800; i++) {
    const a = path[i - 1] as Px;
    const b = path[i] as Px;
    const len = Math.hypot(b.x - a.x, b.y - a.y);
    if (len === 0) continue;
    let at = gap - carry;
    while (at <= len && out.length < 800) {
      const t = at / len;
      out.push({ x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t });
      at += gap;
    }
    carry = len - (at - gap);
  }
  return out;
}

/** One filled dot at a place given in pixels of a plot `w` by `h`. `lit` is 0 to 1 (a partly lit top dot). */
export function Dot({
  x,
  y,
  w,
  h,
  lit = 1,
  kind = 'on',
  r = DOT_R,
}: {
  x: number;
  y: number;
  w: number;
  h: number;
  lit?: number | undefined;
  kind?: 'on' | 'off' | 'band' | undefined;
  r?: number | undefined;
}) {
  return (
    <circle
      className="ds-dot"
      data-dot={kind}
      cx={`${(x / w) * 100}%`}
      cy={`${(y / h) * 100}%`}
      r={r}
      {...(lit < 1 ? { fillOpacity: Math.max(0.25, lit) } : {})}
    />
  );
}

/** The overlay that holds the dots of one chart: it covers the plot and ignores the pointer. */
export function DotLayer({
  className,
  children,
}: {
  className: string;
  children: ReactNode;
}) {
  return (
    <svg className={className} aria-hidden="true" focusable="false">
      {children}
    </svg>
  );
}
