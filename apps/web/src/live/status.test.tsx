// R5.6 (UX-58): a status is a word, a glyph and a colour; the word is always there. The glyph and colour set is the
// shared UI package's Status; the words of a job come from jobStatus.
import { STATUS_GLYPH, Status } from '@crsp/ui';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { jobStatus } from '../screens/jobs/words';

afterEach(cleanup);

describe('status word, glyph and colour (R5.6, UX-58)', () => {
  it('has the glyphs of UX-58', () => {
    expect(STATUS_GLYPH.ok).toBe('✓');
    expect(STATUS_GLYPH.waiting).toBe('◷');
    expect(STATUS_GLYPH.failed).toBe('✕');
    expect(STATUS_GLYPH.info).toBe('ⓘ');
    expect(STATUS_GLYPH.running).toBe('▶');
  });

  it('draws the word with the glyph, and the kind names the colour', () => {
    render(<Status kind="failed" word="failed" />);
    const el = screen.getByText('failed').closest('.ds-status');
    expect(el?.getAttribute('data-status')).toBe('failed');
    expect(el?.textContent).toContain('✕');
    expect(screen.getByText('✕').getAttribute('aria-hidden')).toBe('true');
  });

  it('gives every state and result a word, so colour never stands alone', () => {
    const cases = [
      [{ state: 'queued' }, 'waiting', 'waiting'],
      [{ state: 'running' }, 'running', 'running'],
      [{ state: 'done', result: 'success' }, 'ok', 'success'],
      [{ state: 'done', result: 'failure' }, 'failed', 'failure'],
      [{ state: 'done', result: 'timed_out' }, 'failed', 'timed out'],
      [{ state: 'done', result: 'lost' }, 'failed', 'lost'],
      [{ state: 'done', result: 'cancelled' }, 'off', 'cancelled'],
    ] as const;
    for (const [job, kind, word] of cases) {
      const s = jobStatus(job);
      expect(s.kind).toBe(kind);
      expect(s.word).toBe(word);
      expect(s.word.length).toBeGreaterThan(0);
      expect(STATUS_GLYPH[s.kind]).toBeTruthy();
    }
  });
});
