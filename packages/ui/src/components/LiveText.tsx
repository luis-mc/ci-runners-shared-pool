// Component 84: live text. A number or bar that changes while a person watches has a text equivalent that a
// screen reader announces, at most once per 10 seconds per region (UX-17, observability R6.13). The page passes the
// sentence; the part holds the limit.
import { useEffect, useRef, useState } from 'react';

/** The least time between two announcements of one region. */
export const LIVE_TEXT_GAP_MS = 10_000;

/**
 * The text to announce: the first change goes out at once; later changes wait until `gap` has passed since the
 * last announcement and then only the newest text goes out, so a burst of changes is one announcement.
 */
export function useThrottledText(text: string, gap: number = LIVE_TEXT_GAP_MS): string {
  const [spoken, setSpoken] = useState(text);
  const spokenRef = useRef(text);
  const lastAt = useRef<number>(Number.NEGATIVE_INFINITY);
  useEffect(() => {
    if (text === spokenRef.current) return undefined;
    const say = () => {
      spokenRef.current = text;
      lastAt.current = Date.now();
      setSpoken(text);
    };
    const wait = lastAt.current + gap - Date.now();
    if (wait <= 0) {
      say();
      return undefined;
    }
    const timer = setTimeout(say, wait);
    return () => clearTimeout(timer);
  }, [text, gap]);
  return spoken;
}

/**
 * A polite live region, hidden from the eye, that says `text` when it changes: "3 running, 2 queued". Put it
 * beside the numbers it describes; the numbers themselves are not live. The first text is not announced.
 */
export function LiveText({ text, gap }: { text: string; gap?: number | undefined }) {
  const spoken = useThrottledText(text, gap);
  return (
    <p
      className="ds-visually-hidden"
      aria-live="polite"
      aria-atomic="true"
      data-live-text
    >
      {spoken}
    </p>
  );
}
