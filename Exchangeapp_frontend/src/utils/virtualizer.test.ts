import { describe, expect, it } from 'vitest';
import type { VirtualItem } from '@tanstack/vue-virtual';
import { virtualItemsWithInitialFallback } from './virtualizer';

describe('virtualItemsWithInitialFallback', () => {
  it('returns existing virtual items unchanged', () => {
    const existing: VirtualItem[] = [{
      key: 'a',
      index: 0,
      start: 0,
      end: 100,
      size: 100,
      lane: 0,
    }];

    expect(virtualItemsWithInitialFallback(existing, 1, 'a', 100)).toBe(existing);
  });

  it('does not add a fallback for an empty row set', () => {
    expect(virtualItemsWithInitialFallback([], 0, undefined, 120)).toEqual([]);
  });

  it('does not add a fallback when the first row key is missing', () => {
    expect(virtualItemsWithInitialFallback([], 1, undefined, 120)).toEqual([]);
  });

  it('creates a synthetic first item when rows exist but virtual items are empty', () => {
    expect(virtualItemsWithInitialFallback([], 3, 'first-row', 120)).toEqual([{
      key: 'first-row',
      index: 0,
      start: 0,
      end: 120,
      size: 120,
      lane: 0,
    }]);
  });

  it('supports numeric row keys', () => {
    expect(virtualItemsWithInitialFallback([], 2, 42, 96)[0]?.key).toBe(42);
  });
});
