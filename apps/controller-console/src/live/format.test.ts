// R6.12 (UX-18): the API sends times in UTC; the console shows them in local 24-hour time, year-month-day. The one
// formatter is the shared UI package's (`@crsp/ui` format); the console has no format of its own.
import { formatDate, formatDateTime, formatTime } from '@crsp/ui';
import { describe, expect, it } from 'vitest';

describe('one time format (R6.12, UX-18)', () => {
  // The local moment 2026-11-02 14:30, sent by the API as a UTC instant.
  const local = new Date(2026, 10, 2, 14, 30, 0);
  const wire = local.toISOString();

  it('reads a UTC wire time and shows local time, 24 hours, year-month-day', () => {
    expect(wire.endsWith('Z')).toBe(true);
    expect(formatDate(wire)).toBe('2026-11-02');
    expect(formatTime(wire)).toBe('14:30');
    expect(formatDateTime(wire)).toBe('2026-11-02 14:30');
  });

  it('never writes am or pm', () => {
    const afternoon = new Date(2026, 10, 2, 15, 5).toISOString();
    expect(formatTime(afternoon)).toBe('15:05');
    expect(formatDateTime(afternoon)).not.toMatch(/am|pm/i);
  });

  it('pads single digits', () => {
    expect(formatDateTime(new Date(2026, 0, 3, 4, 5).toISOString())).toBe(
      '2026-01-03 04:05',
    );
  });
});
