// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import ReplyList from './ReplyList.vue';

type ObserverRecord = {
  callback: IntersectionObserverCallback;
  options?: IntersectionObserverInit;
  targets: Element[];
  disconnected: boolean;
};

const observerRecords: ObserverRecord[] = [];

class TestIntersectionObserver {
  private readonly record: ObserverRecord;

  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    this.record = {
      callback,
      options,
      targets: [],
      disconnected: false,
    };
    observerRecords.push(this.record);
  }

  observe(target: Element) {
    this.record.targets.push(target);
  }

  unobserve() {}

  disconnect() {
    this.record.disconnected = true;
  }

  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }
}

const mountReplyList = (overrides: Record<string, unknown> = {}) => mount(ReplyList, {
  props: {
    replies: [],
    currentIdentity: null,
    deletingReplyId: null,
    hasNext: true,
    loadingMore: false,
    loadMoreError: '',
    ...overrides,
  },
  global: {
    stubs: {
      ReplyItem: true,
    },
  },
});

afterEach(() => {
  vi.unstubAllGlobals();
  observerRecords.splice(0);
});

describe('ReplyList pagination loading', () => {
  it('keeps automatic intersection loading enabled by default', async () => {
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    const wrapper = mountReplyList();
    await flushPromises();

    expect(observerRecords).toHaveLength(1);
    expect(observerRecords[0].options?.rootMargin).toBe('240px 0px');
    expect(observerRecords[0].targets).toHaveLength(1);

    observerRecords[0].callback(
      [{ isIntersecting: true } as IntersectionObserverEntry],
      {} as IntersectionObserver,
    );

    expect(wrapper.emitted('loadMore')).toHaveLength(1);
    wrapper.unmount();
    expect(observerRecords[0].disconnected).toBe(true);
  });

  it('disables automatic loading while preserving manual load-more and retry', async () => {
    vi.stubGlobal('IntersectionObserver', TestIntersectionObserver);
    const wrapper = mountReplyList({ autoLoad: false });
    await flushPromises();

    expect(observerRecords).toHaveLength(0);
    await wrapper.get('.reply-list__load-more').trigger('click');
    expect(wrapper.emitted('loadMore')).toHaveLength(1);

    await wrapper.setProps({ loadMoreError: 'temporary failure' });
    await wrapper.get('.reply-list__retry').trigger('click');
    expect(wrapper.emitted('retry')).toHaveLength(1);
    expect(observerRecords).toHaveLength(0);
  });
});
