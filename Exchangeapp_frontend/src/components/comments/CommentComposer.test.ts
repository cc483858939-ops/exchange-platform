// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import CommentComposer from './CommentComposer.vue';
import type { PublicAuthor } from '../../types/User';

const author = (overrides: Partial<PublicAuthor> = {}): PublicAuthor => ({
  id: 7,
  username: 'alice',
  display_name: 'Alice',
  avatar_url: 'https://example.test/alice.jpg',
  ...overrides,
});

describe('CommentComposer avatar and reply behavior', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('renders the real avatar when the profile provides an avatar URL', () => {
    wrapper = mount(CommentComposer, { props: { author: author() } });

    expect(wrapper.get('.comment-composer__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice.jpg');
  });

  it('uses the display-name initial when the avatar is missing', () => {
    wrapper = mount(CommentComposer, {
      props: { author: author({ avatar_url: '', display_name: 'Universal' }) },
    });

    expect(wrapper.find('.comment-composer__avatar .user-avatar__image').exists()).toBe(false);
    expect(wrapper.get('.comment-composer__avatar .user-avatar__fallback').text()).toBe('U');
  });

  it('uses the username initial when the display name is empty', () => {
    wrapper = mount(CommentComposer, {
      props: { author: author({ avatar_url: '', display_name: '', username: '12345678' }) },
    });

    expect(wrapper.find('.comment-composer__avatar .user-avatar__image').exists()).toBe(false);
    expect(wrapper.get('.comment-composer__avatar .user-avatar__fallback').text()).toBe('1');
  });

  it('replaces a broken avatar image with the initial fallback', async () => {
    wrapper = mount(CommentComposer, { props: { author: author() } });

    await wrapper.get('.comment-composer__avatar .user-avatar__image').trigger('error');

    expect(wrapper.find('.comment-composer__avatar .user-avatar__image').exists()).toBe(false);
    expect(wrapper.get('.comment-composer__avatar .user-avatar__fallback').text()).toBe('A');
  });

  it('resets the avatar failure when the avatar URL changes', async () => {
    wrapper = mount(CommentComposer, { props: { author: author() } });
    await wrapper.get('.comment-composer__avatar .user-avatar__image').trigger('error');

    await wrapper.setProps({
      author: author({ avatar_url: 'https://example.test/alice-new.jpg' }),
    });

    expect(wrapper.get('.comment-composer__avatar .user-avatar__image').attributes('src'))
      .toBe('https://example.test/alice-new.jpg');
  });

  it('renders the controlled model value', () => {
    wrapper = mount(CommentComposer, {
      props: { author: author(), modelValue: 'restored draft' },
    });

    expect(wrapper.get('textarea').element.value).toBe('restored draft');
  });

  it('emits model updates when the user edits the textarea', async () => {
    wrapper = mount(CommentComposer, { props: { author: author(), modelValue: '' } });

    await wrapper.get('textarea').setValue('hello');

    expect(wrapper.emitted('update:modelValue')).toEqual([['hello']]);
  });

  it('resizes after an external multiline draft restore', async () => {
    wrapper = mount(CommentComposer, { props: { author: author(), modelValue: '' } });
    const textarea = wrapper.get('textarea').element as HTMLTextAreaElement;
    Object.defineProperty(textarea, 'scrollHeight', {
      configurable: true,
      value: 84,
    });

    await wrapper.setProps({ modelValue: 'line 1\nline 2' });
    await nextTick();

    expect(textarea.style.height).toBe('84px');
  });

  it('accepts reply content and emits the trimmed value', async () => {
    wrapper = mount(CommentComposer, {
      props: { author: author(), modelValue: '  useful reply  ' },
    });

    expect(wrapper.get('textarea').attributes('placeholder')).toBe('Post your reply...');
    expect(wrapper.find('.comment-composer__hint').exists()).toBe(false);

    await wrapper.get('form').trigger('submit');

    expect(wrapper.emitted('submit')).toEqual([['useful reply']]);
  });

  it('keeps the 1000-character validation while presenting a reply row', async () => {
    wrapper = mount(CommentComposer, {
      props: { author: author(), modelValue: 'a'.repeat(1001) },
    });

    expect(wrapper.get('.comment-composer__validation').text())
      .toBe('1001/1000 characters. Please shorten your reply.');
    expect(wrapper.get('button').attributes('disabled')).toBeDefined();
  });

  it('keeps clear as an exposed controlled-input command', async () => {
    wrapper = mount(CommentComposer, {
      props: { author: author(), modelValue: 'draft' },
    });

    (wrapper.vm as unknown as { clear: () => void }).clear();
    await wrapper.setProps({ modelValue: '' });

    expect(wrapper.emitted('update:modelValue')).toEqual([['']]);
    expect(wrapper.get('textarea').element.value).toBe('');
  });
});
