import { afterEach, describe, expect, it, vi } from 'vitest';
import { lw, type LifecycleWord } from './lifecycleWords';

afterEach(() => vi.restoreAllMocks());

describe('lw', () => {
  it('fills values', () => {
    expect(lw('up_to_date', { version: '1.2.3' })).toBe(
      'Up to date. Version 1.2.3 is the newest.',
    );
  });

  it('never throws on an unknown key: it shows the key and warns once', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const key = 'state.not_a_word' as LifecycleWord;
    expect(lw(key)).toBe('state.not_a_word');
    expect(lw(key)).toBe('state.not_a_word');
    expect(warn).toHaveBeenCalledTimes(1);
  });
});
