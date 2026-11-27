import { describe, expect, it } from 'vitest';
import { detectOs, putSystemFirst } from './detect.ts';

const UA = {
  win: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126 Safari/537.36',
  mac: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15',
  linux: 'Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0',
  phone:
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Mobile Safari/537.36',
  iphone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15',
};

describe('system detection (website R5.9, T3)', () => {
  it('picks Windows, macOS and Linux from sample user agents', () => {
    expect(detectOs(UA.win)).toBe('windows');
    expect(detectOs(UA.mac)).toBe('macos');
    expect(detectOs(UA.linux)).toBe('linux');
  });
  it('lists every system for a phone or an unknown agent', () => {
    expect(detectOs(UA.phone)).toBeNull();
    expect(detectOs(UA.iphone)).toBeNull();
    expect(detectOs('curl/8')).toBeNull();
  });
  it('moves the visitor system card to the front', () => {
    const order = ['os-windows', 'os-macos', 'os-linux'];
    const parent = {
      firstElementChild: null as unknown,
      insertBefore(card: { id: string }) {
        order.splice(order.indexOf(card.id), 1);
        order.unshift(card.id);
      },
    };
    const cards = new Map(order.map((id) => [id, { id, parentElement: parent }]));
    parent.firstElementChild = cards.get('os-windows');
    const doc = {
      getElementById: (id: string) => cards.get(id) ?? null,
    } as unknown as Document;
    expect(putSystemFirst(doc, UA.linux)).toBe('linux');
    expect(order[0]).toBe('os-linux');
    expect(putSystemFirst(doc, UA.phone)).toBeNull();
  });
});
