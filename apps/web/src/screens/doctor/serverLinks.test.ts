import { describe, expect, it } from 'vitest';
import { consoleAddress } from './serverLinks';

describe('addresses the controller sends', () => {
  it.each([
    ['/machines/m9', '/setup/machines/m9'],
    ['/runners', '/setup/runners'],
    ['/pool/runners', '/setup/runners'],
    ['/repositories/12', '/setup/pool/12'],
    ['/caches', '/setup/images?tab=caches'],
    ['/doctor', '/doctor'],
    ['/setup/machines/m1', '/setup/machines/m1'],
  ])('%s opens %s', (from, to) => {
    expect(consoleAddress(from)).toBe(to);
  });
});
