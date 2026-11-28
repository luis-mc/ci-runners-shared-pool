import { useEffect, useState } from 'react';

/** True on a phone-width screen (below 601 px, the design system's phone break). No matchMedia: wide. */
export function useNarrow(): boolean {
  const query = '(max-width: 600px)';
  const [narrow, setNarrow] = useState(
    () => typeof window.matchMedia === 'function' && window.matchMedia(query).matches,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const m = window.matchMedia(query);
    const on = () => setNarrow(m.matches);
    on();
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  return narrow;
}
