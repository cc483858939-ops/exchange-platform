// @vitest-environment jsdom

import { defineComponent, h, nextTick } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { useNow } from './useNow';

const ClockConsumer = defineComponent({
  name: 'ClockConsumer',
  setup() {
    const now = useNow();

    return () => h('span', now.value.toISOString());
  },
});

const ClockHost = defineComponent({
  props: {
    count: {
      type: Number,
      required: true,
    },
  },
  setup(props) {
    return () => h(
      'div',
      Array.from({ length: props.count }, (_, index) =>
        h(ClockConsumer, { key: index }),
      ),
    );
  },
});

describe('useNow', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 7, 19, 17, 0, 0));
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('starts one shared interval for two consumers', () => {
    const setIntervalSpy = vi.spyOn(window, 'setInterval');

    wrapper = mount(ClockHost, { props: { count: 2 } });

    expect(setIntervalSpy).toHaveBeenCalledTimes(1);
    expect(setIntervalSpy).toHaveBeenCalledWith(expect.any(Function), 30_000);
  });

  it('keeps the interval alive while one of two consumers remains mounted', async () => {
    const clearIntervalSpy = vi.spyOn(window, 'clearInterval');

    wrapper = mount(ClockHost, { props: { count: 2 } });
    await wrapper.setProps({ count: 1 });

    expect(clearIntervalSpy).not.toHaveBeenCalled();
  });

  it('clears the interval after the final consumer unmounts', async () => {
    const clearIntervalSpy = vi.spyOn(window, 'clearInterval');

    wrapper = mount(ClockHost, { props: { count: 2 } });
    await wrapper.setProps({ count: 0 });

    expect(clearIntervalSpy).toHaveBeenCalledTimes(1);
  });

  it('updates immediately when the document becomes visible', async () => {
    wrapper = mount(ClockHost, { props: { count: 1 } });
    const initialValue = wrapper.find('span').text();

    vi.setSystemTime(new Date(2026, 7, 19, 17, 5, 0));
    document.dispatchEvent(new Event('visibilitychange'));
    await nextTick();

    expect(initialValue).not.toBe(wrapper.find('span').text());
    expect(wrapper.find('span').text()).toBe(new Date(2026, 7, 19, 17, 5, 0).toISOString());
  });
});
