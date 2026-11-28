// CRSP399: the text size choice moves the one root scale token through data-text-size.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { applyTextSize, savedTextSize, TEXT_SIZE_STEPS } from './themes';

afterEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-text-size');
});
const css = readFileSync(join(import.meta.dirname, 'tokens.css'), 'utf8');

describe('text size', () => {
  it('has six steps around the base', () => {
    expect([...TEXT_SIZE_STEPS]).toEqual([80, 90, 100, 110, 120, 130]);
  });
  it('applies a step, keeps it, and clears the attribute at 100', () => {
    applyTextSize(120);
    expect(document.documentElement.getAttribute('data-text-size')).toBe('120');
    expect(savedTextSize()).toBe(120);
    applyTextSize(100);
    expect(document.documentElement.hasAttribute('data-text-size')).toBe(false);
    expect(savedTextSize()).toBe(100);
  });
  it('ignores a value that is not a step', () => {
    window.localStorage.setItem('crsp.text-size', '95');
    expect(savedTextSize()).toBe(100);
    applyTextSize(95);
    expect(document.documentElement.hasAttribute('data-text-size')).toBe(false);
  });
  it('maps each step to the base factor 0.85 times the percent', () => {
    for (const [pct, v] of [
      [80, 0.68],
      [90, 0.765],
      [110, 0.935],
      [120, 1.02],
      [130, 1.105],
    ] as const) {
      const at = css.indexOf(`:root[data-text-size='${pct}'] {`);
      expect(at).toBeGreaterThan(0);
      const m = /--ds-type-scale: ([0-9.]+);/.exec(css.slice(at, at + 80));
      expect(Number(m?.[1])).toBeCloseTo(v, 6);
      expect(v).toBeCloseTo((0.85 * pct) / 100, 6);
    }
  });
});
