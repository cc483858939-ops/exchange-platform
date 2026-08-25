// @vitest-environment jsdom

import { nextTick } from 'vue';
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import UserAvatar from './UserAvatar.vue';

describe('UserAvatar', () => {
  it('renders a fallback immediately when no avatar URL exists', () => {
    const wrapper = mount(UserAvatar, {
      props: { displayName: 'Alice Smith', username: 'alice' },
    });

    expect(wrapper.find('.user-avatar__fallback').text()).toBe('A');
    expect(wrapper.find('img').exists()).toBe(false);
  });

  it('keeps the fallback visible while an image is pending and shows the image after load', async () => {
    const wrapper = mount(UserAvatar, {
      props: { avatarUrl: '/alice.webp', displayName: 'Alice', decorative: true },
    });
    const image = wrapper.get('.user-avatar__image');

    expect(wrapper.get('.user-avatar__fallback').text()).toBe('A');
    expect(image.classes()).not.toContain('user-avatar__image--loaded');
    expect(image.attributes('alt')).toBe('');
    expect(wrapper.attributes('aria-hidden')).toBe('true');

    await image.trigger('load');
    expect(image.classes()).toContain('user-avatar__image--loaded');
  });

  it('returns to the fallback when an image fails', async () => {
    const wrapper = mount(UserAvatar, {
      props: { avatarUrl: '/broken.webp', displayName: 'Alice' },
    });

    await wrapper.get('img').trigger('error');

    expect(wrapper.find('img').exists()).toBe(false);
    expect(wrapper.get('.user-avatar__fallback').text()).toBe('A');
  });

  it('resets image state when the URL or identity changes', async () => {
    const wrapper = mount(UserAvatar, {
      props: { avatarUrl: '/broken.webp', displayName: 'Alice' },
    });
    await wrapper.get('img').trigger('error');

    await wrapper.setProps({ avatarUrl: '/bob.webp', displayName: 'Bob' });
    await nextTick();

    expect(wrapper.get('img').attributes('src')).toBe('/bob.webp');
    expect(wrapper.get('.user-avatar__fallback').text()).toBe('B');
  });

  it('uses the first Unicode code point and then the documented fallbacks', async () => {
    const wrapper = mount(UserAvatar, {
      props: { displayName: ' 你好吗', username: 'alice' },
    });
    expect(wrapper.get('.user-avatar__fallback').text()).toBe('你');

    await wrapper.setProps({ displayName: '', username: 'bob' });
    expect(wrapper.get('.user-avatar__fallback').text()).toBe('B');

    await wrapper.setProps({ displayName: '', username: '' });
    expect(wrapper.get('.user-avatar__fallback').text()).toBe('?');
  });

  it('applies the requested size and non-decorative alt text', () => {
    const wrapper = mount(UserAvatar, {
      props: {
        avatarUrl: '/alice.webp',
        displayName: 'Alice',
        size: 36,
        alt: 'Alice profile photo',
      },
    });

    expect(wrapper.attributes('style')).toContain('--user-avatar-size: 36px');
    expect(wrapper.get('img').attributes('alt')).toBe('Alice profile photo');
    expect(wrapper.attributes('role')).toBe('img');
  });
});
