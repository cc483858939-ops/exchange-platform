// @vitest-environment jsdom

import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import type { Post } from '../../types/Post';
import ReplyItem from './ReplyItem.vue';

const makeReply = (overrides: Partial<Post> = {}): Post => ({
  id: 42,
  created_at: '2026-08-27T13:42:00',
  updated_at: '2026-08-27T13:42:00',
  published_at: '2026-08-27T13:42:00',
  author: {
    id: 7,
    username: 'reply-author',
    display_name: 'Reply Author',
    avatar_url: '',
  },
  content: 'Reply body',
  conversation_id: 42,
  reply_to_post_id: 42,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 0,
  reply_count: 0,
  view_count: 0,
  deleted: false,
  ...overrides,
});

const mountReply = (props: Record<string, unknown> = {}) => mount(ReplyItem, {
  props: {
    reply: makeReply(),
    canDelete: true,
    deleting: false,
    ...props,
  },
  global: {
    stubs: {
      AuthorIdentity: { template: '<span class="test-author" />' },
      LinkifiedText: { props: ['text'], template: '<span>{{ text }}</span>' },
      AppIcon: { props: ['name'], template: '<span class="test-icon" :data-icon="name" />' },
      PostMediaGrid: {
        props: ['media', 'interactive'],
        emits: ['open'],
        template: '<button class="test-media-grid" type="button" @click="$emit(\'open\', 0)" />',
      },
    },
  },
});

describe('ReplyItem', () => {
  it('only renders the delete trigger for an owned reply', () => {
    expect(mountReply({ canDelete: true }).find('.reply-item__delete').exists()).toBe(true);
    expect(mountReply({ canDelete: false }).find('.reply-item__delete').exists()).toBe(false);
  });

  it('uses a 44px-target delete control with the accessible label', () => {
    const wrapper = mountReply();
    const button = wrapper.get('.reply-item__delete');

    expect(button.attributes('aria-label')).toBe('Delete reply');
    expect(button.classes()).toContain('reply-item__delete');
  });

  it('emits requestDelete instead of authorizing deletion itself', async () => {
    const wrapper = mountReply();

    await wrapper.get('.reply-item__delete').trigger('click');

    expect(wrapper.emitted('requestDelete')).toEqual([[42]]);
    expect(wrapper.emitted('delete')).toBeUndefined();
  });

  it('disables the delete trigger while the reply is deleting', () => {
    const wrapper = mountReply({ deleting: true });

    expect(wrapper.get('.reply-item__delete').attributes('disabled')).toBe('');
    expect(wrapper.find('.reply-item__status').text()).toBe('Deleting...');
  });

  it('preserves reply media activation', async () => {
    const replyMedia = [{ type: 'image' as const, url: '/reply.png', position: 0 }];
    const wrapper = mountReply({ reply: makeReply({ media: replyMedia }) });

    await wrapper.get('.test-media-grid').trigger('click');

    expect(wrapper.emitted('openMedia')).toEqual([[replyMedia, 0]]);
  });
});
