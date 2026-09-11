// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { defineComponent, nextTick, ref } from 'vue';
import { beforeEach, describe, expect, it } from 'vitest';
import { usePageTitle } from './usePageTitle';

describe('usePageTitle', () => {
  beforeEach(() => {
    document.title = 'Exchange';
  });

  it('writes the latest title only while the source is active', async () => {
    const source = ref('@alice');
    const active = ref(true);
    const Host = defineComponent({
      setup() {
        usePageTitle(source, active);
        return () => null;
      },
    });
    const wrapper = mount(Host);

    expect(document.title).toBe('@alice — Exchange');

    active.value = false;
    document.title = 'Post — Exchange';
    source.value = '@alice2';
    await nextTick();
    expect(document.title).toBe('Post — Exchange');

    active.value = true;
    await nextTick();
    expect(document.title).toBe('@alice2 — Exchange');
    wrapper.unmount();
  });

  it('keeps the original source-only behavior when active is omitted', async () => {
    const source = ref('@alice');
    const Host = defineComponent({
      setup() {
        usePageTitle(source);
        return () => null;
      },
    });
    const wrapper = mount(Host);

    expect(document.title).toBe('@alice — Exchange');
    source.value = '@bob';
    await nextTick();
    expect(document.title).toBe('@bob — Exchange');
    wrapper.unmount();
  });
});
