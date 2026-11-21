// A date and time on one line, everywhere in the console (LOOK49, UX-78). The browser may break "2026-11-21 14:08"
// at a hyphen or the space; this part never lets it. Pages show a moment with this part, not a bare string.
import { formatDateTime } from './format';

export function DateTime({ value }: { value: string | number | Date }) {
  const d = new Date(value);
  return (
    <time
      className="ds-datetime"
      dateTime={Number.isNaN(d.getTime()) ? undefined : d.toISOString()}
    >
      {formatDateTime(value)}
    </time>
  );
}
