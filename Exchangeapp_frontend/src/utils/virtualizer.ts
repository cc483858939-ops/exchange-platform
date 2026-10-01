import type { VirtualItem } from '@tanstack/vue-virtual';

export const virtualItemsWithInitialFallback = (
  virtualItems: VirtualItem[],
  rowCount: number,
  firstRowKey: VirtualItem['key'] | undefined,
  estimateSize: number,
): VirtualItem[] => {
  if (
    virtualItems.length > 0
    || rowCount === 0
    || firstRowKey === undefined
  ) {
    return virtualItems;
  }

  return [{
    key: firstRowKey,
    index: 0,
    start: 0,
    end: estimateSize,
    size: estimateSize,
    lane: 0,
  }];
};
