// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { afterEach, describe, expect, it } from 'vitest';
import type { VueWrapper } from '@vue/test-utils';
import type { FeedTab } from '../../types/Feed';
import FeedTabs from './FeedTabs.vue';

describe('FeedTabs activation intents', () => {
  let wrapper: VueWrapper | null = null;

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  const mountTabs = (activeTab: FeedTab) => {
    wrapper = mount(FeedTabs, {
      props: { activeTab },
    });
    return wrapper;
  };

  it('emits reselect instead of select for the active For You tab', async () => {
    const tabs = mountTabs('for-you');

    await tabs.get('[data-feed-tab="for-you"]').trigger('click');

    expect(tabs.emitted('reselect')).toEqual([['for-you']]);
    expect(tabs.emitted('select')).toBeUndefined();
  });

  it('emits select for an inactive Following tab', async () => {
    const tabs = mountTabs('for-you');

    await tabs.get('[data-feed-tab="following"]').trigger('click');

    expect(tabs.emitted('select')).toEqual([['following']]);
    expect(tabs.emitted('reselect')).toBeUndefined();
  });

  it('emits reselect instead of select for the active Following tab', async () => {
    const tabs = mountTabs('following');

    await tabs.get('[data-feed-tab="following"]').trigger('click');

    expect(tabs.emitted('reselect')).toEqual([['following']]);
    expect(tabs.emitted('select')).toBeUndefined();
  });

  it.each([
    ['for-you', 'for-you', 'Home'],
    ['following', 'following', 'End'],
  ] as Array<[FeedTab, FeedTab, string]>)
    ('keeps %s keyboard Home/End navigation as select, not reselect', async (activeTab, expectedTab, key) => {
      const tabs = mountTabs(activeTab);

      await tabs.get(`[data-feed-tab="${activeTab}"]`).trigger('keydown', { key });

      expect(tabs.emitted('select')).toEqual([[expectedTab]]);
      expect(tabs.emitted('reselect')).toBeUndefined();
    });

  it.each([
    ['for-you', 'ArrowRight', 'following'],
    ['following', 'ArrowLeft', 'for-you'],
    ['following', 'Home', 'for-you'],
    ['for-you', 'End', 'following'],
  ] as Array<[FeedTab, string, FeedTab]>)
    ('moves from %s with %s using select(%s)', async (activeTab, key, expectedTab) => {
      const tabs = mountTabs(activeTab);

      await tabs.get(`[data-feed-tab="${activeTab}"]`).trigger('keydown', { key });

      expect(tabs.emitted('select')).toEqual([[expectedTab]]);
      expect(tabs.emitted('reselect')).toBeUndefined();
    });
});
