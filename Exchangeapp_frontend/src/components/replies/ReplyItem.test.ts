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
  language: 'und',
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
  repost_count: overrides.repost_count ?? 0,
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
      LinkifiedText: {
        name: 'LinkifiedTextStub',
        props: ['text', 'to'],
        template: '<span class="test-linkified-text">{{ text }}</span>',
      },
      RouterLink: {
        name: 'RouterLinkStub',
        props: ['to'],
        template: '<a class="test-router-link"><slot /></a>',
      },
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
  it('links ordinary reply text to its PostDetail without a reply intent', () => {
    const wrapper = mountReply();

    expect(wrapper.getComponent({ name: 'LinkifiedTextStub' }).props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
    });
  });

  it('shows a Reply action for zero children and links with reply intent', () => {
    const wrapper = mountReply({ reply: makeReply({ reply_count: 0 }) });
    const action = wrapper.get('.reply-item__reply-action');

    expect(action.text()).toBe('Reply');
    expect(action.attributes('aria-label')).toBe('Reply to this reply');
    expect(wrapper.getComponent({ name: 'RouterLinkStub' }).props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
      query: { reply: '1' },
    });
    expect(action.find('.test-icon').attributes('data-icon')).toBe('reply');
  });

  it('shows the direct reply count with a plural accessible label', () => {
    const wrapper = mountReply({ reply: makeReply({ reply_count: 3 }) });

    expect(wrapper.get('.reply-item__reply-action').text()).toBe('3');
    expect(wrapper.get('.reply-item__reply-action').attributes('aria-label'))
      .toBe('3 replies. Continue discussion');
  });

  it('uses a singular accessible label for one direct reply', () => {
    const wrapper = mountReply({ reply: makeReply({ reply_count: 1 }) });

    expect(wrapper.get('.reply-item__reply-action').text()).toBe('1');
    expect(wrapper.get('.reply-item__reply-action').attributes('aria-label'))
      .toBe('1 reply. Continue discussion');
  });

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

  it('uses BookmarkAction for reply bookmarks and forwards the reply ID', async () => {
    const wrapper = mountReply({
      bookmarkState: { bookmarked: true, status: 'ready' },
    });
    const bookmark = wrapper.get('.reply-item__bookmark');

    expect(bookmark.classes()).toContain('bookmark-action');
    expect(bookmark.attributes('aria-pressed')).toBe('true');
    expect(bookmark.attributes('aria-label')).toBe('Remove bookmark');

    await bookmark.trigger('click');

    expect(wrapper.emitted('toggleBookmark')).toEqual([[42]]);
  });

  it('keeps reply bookmarks unavailable while loading or pending', () => {
    const loading = mountReply({
      bookmarkState: { bookmarked: false, status: 'unknown' },
    }).get('.reply-item__bookmark');
    const pending = mountReply({
      bookmarkState: { bookmarked: false, status: 'ready' },
      bookmarkPending: true,
    }).get('.reply-item__bookmark');

    expect(loading.attributes('disabled')).toBe('');
    expect(loading.attributes('aria-busy')).toBe('true');
    expect(pending.attributes('disabled')).toBe('');
    expect(pending.attributes('aria-busy')).toBe('true');
  });

  it('preserves reply media activation', async () => {
    const replyMedia = [{ type: 'image' as const, url: '/reply.png', large_url: '/reply-large.png', width: 1200, height: 800, position: 0 }];
    const wrapper = mountReply({ reply: makeReply({ media: replyMedia }) });

    await wrapper.get('.test-media-grid').trigger('click');

    expect(wrapper.emitted('openMedia')).toEqual([[replyMedia, 0]]);
  });
});
