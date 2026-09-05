// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, RouterLinkStub } from '@vue/test-utils';
import PostCard from './PostCard.vue';
import LikeAction from '../engagement/LikeAction.vue';
import RepostAction from '../engagement/RepostAction.vue';
import type { FeedPost } from '../../types/Feed';
import { formatCompactEngagementCount } from '../../utils/engagementCount';

const mocks = vi.hoisted(() => ({
  observeFeedCard: vi.fn(),
  unobserveFeedCard: vi.fn(),
  enqueue: vi.fn(),
  remember: vi.fn(),
}));

vi.mock('../../services/postViewTelemetry', () => ({
  getPostViewTelemetry: () => mocks,
}));

vi.mock('../../store/postDetailHandoff', () => ({
  usePostDetailHandoffStore: () => ({ remember: mocks.remember }),
}));

vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve: () => ({ href: '/posts/42' }) }),
}));

const basePost = (): FeedPost => ({
  id: 42,
  author: {
    id: 7,
    username: 'reader',
    display_name: 'Reader',
    avatar_url: '',
  },
  content: 'Post body',
  media: [],
  createdAt: '2026-08-17T00:00:00.000Z',
  likeCount: 12,
  replyCount: 3,
  viewCount: 1234,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
});

describe('PostCard View metric and telemetry lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  const mountPostCard = (post = basePost(), extraProps: Record<string, unknown> = {}) => mount(PostCard, {
    props: { post, ...extraProps },
    global: {
      stubs: {
        AuthorIdentity: { template: '<span class="author-identity" />' },
        LikeAction: {
          props: ['liked', 'count', 'disabled', 'loading', 'pending', 'ariaLabel', 'ariaPressed', 'variant'],
          emits: ['toggle'],
          template: '<button class="stub-like-action" type="button" :disabled="disabled || loading || pending" @click="$emit(\'toggle\')">{{ count }}</button>',
        },
        AppIcon: {
          props: ['name'],
          template: '<span class="test-icon" :data-icon="name" />',
        },
        RouterLink: RouterLinkStub,
      },
    },
  });

  it('renders a navigable compact View metric with the analytics icon and destination', async () => {
    const post = basePost();
    const wrapper = mountPostCard(post);
    const views = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__views'));

    expect(views?.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
    });
    expect(views?.text()).toContain(formatCompactEngagementCount(1234));
    expect(views?.attributes('aria-label')).toBe('Open post, 1,234 views');
    expect(wrapper.find('[data-icon="analytics"]').exists()).toBe(true);

    await views?.trigger('click');

    expect(mocks.remember).toHaveBeenCalledTimes(1);
    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
    expect(wrapper.emitted('toggleLike') ?? []).toHaveLength(0);
    expect(wrapper.emitted('notInterested') ?? []).toHaveLength(0);
    expect(wrapper.emitted('deletePost') ?? []).toHaveLength(0);
    expect(mocks.enqueue).not.toHaveBeenCalled();
  });

  it('captures content, reply, and view navigation with one handoff and one postClick each', async () => {
    const post = basePost();
    const wrapper = mountPostCard(post);
    const links = wrapper.findAllComponents(RouterLinkStub);
    const content = wrapper.find('.post-card__body .linkified-text__internal');
    const reply = links.find(link => link.classes().includes('post-card__reply'))!;
    const views = links.find(link => link.classes().includes('post-card__views'))!;

    expect(reply.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
      query: { reply: '1' },
    });
    await content.trigger('click');
    await reply.trigger('click');
    await views.trigger('click');

    expect(mocks.remember).toHaveBeenCalledTimes(3);
    expect(mocks.remember).toHaveBeenNthCalledWith(1, post);
    expect(mocks.remember).toHaveBeenNthCalledWith(2, post);
    expect(mocks.remember).toHaveBeenNthCalledWith(3, post);
    expect(wrapper.emitted('postClick')).toEqual([[post], [post], [post]]);
  });

  it('does not remember a modified-click navigation', async () => {
    const post = basePost();
    const wrapper = mountPostCard(post);
    const content = wrapper.find('.post-card__body .linkified-text__internal');

    await content.trigger('click', { ctrlKey: true });

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('separates external URLs from Post Detail navigation', async () => {
    const post = { ...basePost(), content: 'Read https://example.com today' };
    const wrapper = mountPostCard(post);
    const external = wrapper.get('a.linkified-text__external');
    const internal = wrapper.get('a.linkified-text__internal');

    expect(external.attributes('href')).toBe('https://example.com');
    expect(wrapper.findAll('a a')).toHaveLength(0);

    await external.trigger('click');
    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();

    await internal.trigger('click');
    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('keeps media as an independent Post Detail link', async () => {
    const post = {
      ...basePost(),
      media: [{ type: 'image' as const, url: '/media.png', position: 0 }],
    };
    const wrapper = mountPostCard(post);
    const cover = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__media-link'));

    expect(cover?.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
    });

    await cover?.trigger('click');

    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('does not remember non-navigation like activation', async () => {
    const wrapper = mountPostCard();

    await wrapper.find('.stub-like-action').trigger('click');

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('toggleLike')).toEqual([[42]]);
  });

  it('observes on mount, replaces stale observation on id change, and unobserves on unmount', async () => {
    const wrapper = mountPostCard();
    const root = wrapper.element;

    expect(mocks.observeFeedCard).toHaveBeenCalledWith(root, 42);

    await wrapper.setProps({ post: { ...basePost(), id: 43 } });
    expect(mocks.unobserveFeedCard).toHaveBeenCalledWith(root);
    expect(mocks.observeFeedCard).toHaveBeenCalledWith(root, 43);

    wrapper.unmount();
    expect(mocks.unobserveFeedCard).toHaveBeenCalledWith(root);
  });

  it('does not observe feed view telemetry when trackView is false and syncs later changes', async () => {
    const wrapper = mountPostCard(basePost(), { trackView: false });
    const root = wrapper.element;

    expect(mocks.observeFeedCard).not.toHaveBeenCalled();

    await wrapper.setProps({ trackView: true });
    expect(mocks.observeFeedCard).toHaveBeenCalledWith(root, 42);

    await wrapper.setProps({ trackView: false });
    expect(mocks.unobserveFeedCard).toHaveBeenCalledWith(root);
  });

  it('maps like state to LikeAction and forwards its activation to the parent contract', async () => {
    const post = { ...basePost(), liked: true };
    const wrapper = mountPostCard(post);
    const likeAction = wrapper.findComponent(LikeAction);

    expect(likeAction.props('liked')).toBe(true);
    expect(likeAction.props('count')).toBe(12);
    expect(likeAction.props('ariaPressed')).toBe(true);
    expect(likeAction.props('loading')).toBe(false);
    expect(likeAction.props('disabled')).toBe(false);
    expect(likeAction.props('pending')).toBe(false);

    await likeAction.trigger('click');

    expect(wrapper.emitted('toggleLike')).toEqual([[42]]);
  });

  it('renders Reply, Repost, Like, Views and keeps the canonical author under repost context', async () => {
    const post = {
      ...basePost(),
      repostCount: 9,
      reposted: true,
      repostContext: {
        actor: {
          id: 11,
          username: 'alice',
          display_name: 'Alice',
          avatar_url: '',
        },
      },
    };
    const wrapper = mountPostCard(post);
    const repostAction = wrapper.findComponent(RepostAction);
    const engagement = wrapper.find('.post-card__engagement').element.children;

    expect(wrapper.find('.post-card__repost-context').text()).toBe('Alice reposted');
    expect(repostAction.props('reposted')).toBe(true);
    expect(repostAction.props('count')).toBe(9);
    expect(Array.from(engagement).map(element => element.className)).toEqual([
      'post-card__metric post-card__reply',
      'repost-action repost-action--compact repost-action--reposted',
      'stub-like-action',
      'post-card__metric post-card__views',
    ]);

    await repostAction.trigger('click');
    expect(wrapper.emitted('toggleRepost')).toEqual([[42]]);
  });

  it('renders active and tombstoned bounded references', async () => {
    const activeReference = {
      id: 9,
      deleted: false as const,
      author: {
        id: 8,
        username: 'referenced',
        display_name: 'Referenced Author',
        avatar_url: '',
      },
      content: 'Referenced post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [],
    };
    const wrapper = mountPostCard({ ...basePost(), quotePost: activeReference });

    expect(wrapper.find('.post-card__reference-content').text()).toBe('Referenced post body');
    expect(wrapper.find('.post-card__reference-deleted').exists()).toBe(false);
    const referenceBodyLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'));
    expect(referenceBodyLink?.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });

    await wrapper.setProps({
      post: { ...basePost(), quotePost: { id: 9, deleted: true } },
    });
    expect(wrapper.find('.post-card__reference-deleted').text()).toBe('Post unavailable');
    expect(wrapper.find('.post-card__reference-content').exists()).toBe(false);
    expect(wrapper.find('.post-card__reference-deleted a').exists()).toBe(false);
  });

  it('routes quote body and media to the referenced Post without outer handoff', async () => {
    const quotedPost = {
      id: 9,
      deleted: false as const,
      author: {
        id: 8,
        username: 'referenced',
        display_name: 'Referenced Author',
        avatar_url: '',
      },
      content: 'Referenced post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [{ type: 'image' as const, url: '/reference.png', position: 0 }],
    };
    const post = {
      ...basePost(),
      media: [{ type: 'image' as const, url: '/outer.png', position: 0 }],
      quotePost: quotedPost,
    };
    const wrapper = mountPostCard(post);
    const referenceBodyLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'))!;
    const referenceMediaLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__reference-media-link'))!;
    const outerMediaLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__media-link'))!;

    expect(referenceBodyLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });
    expect(referenceMediaLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });
    expect(outerMediaLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '42' },
    });

    await referenceBodyLink.trigger('click');
    await referenceMediaLink.trigger('click');

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();

    await outerMediaLink.trigger('click');
    expect(mocks.remember).toHaveBeenCalledWith(post);
    expect(wrapper.emitted('postClick')).toEqual([[post]]);
  });

  it('routes reply references to the replied-to Post without changing outer navigation', async () => {
    const repliedToPost = {
      id: 17,
      deleted: false as const,
      author: {
        id: 8,
        username: 'parent',
        display_name: 'Parent Author',
        avatar_url: '',
      },
      content: 'Parent post body',
      published_at: '2026-08-17T00:00:00.000Z',
      media: [{ type: 'image' as const, url: '/parent.png', position: 0 }],
    };
    const wrapper = mountPostCard({ ...basePost(), replyToPost: repliedToPost });
    const referenceBodyLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'))!;
    const referenceMediaLink = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('post-card__reference-media-link'))!;

    expect(referenceBodyLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '17' },
    });
    expect(referenceMediaLink.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '17' },
    });

    await referenceBodyLink.trigger('click');
    await referenceMediaLink.trigger('click');

    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();
  });

  it('keeps reference external URLs external and does not emit outer navigation', async () => {
    const wrapper = mountPostCard({
      ...basePost(),
      quotePost: {
        id: 9,
        deleted: false as const,
        author: {
          id: 8,
          username: 'referenced',
          display_name: 'Referenced Author',
          avatar_url: '',
        },
        content: 'Read https://example.com here',
        published_at: '2026-08-17T00:00:00.000Z',
        media: [],
      },
    });
    const external = wrapper.get('.post-card__reference-content .linkified-text__external');
    const internal = wrapper.findAllComponents(RouterLinkStub)
      .find(link => link.classes().includes('linkified-text__internal')
        && link.element.closest('.post-card__reference-content'))!;

    expect(external.attributes('href')).toBe('https://example.com');
    expect(internal.props('to')).toEqual({
      name: 'PostDetail',
      params: { id: '9' },
    });

    await external.trigger('click');
    expect(mocks.remember).not.toHaveBeenCalled();
    expect(wrapper.emitted('postClick')).toBeUndefined();
    expect(wrapper.findAll('a a')).toHaveLength(0);
  });

  it('maps unknown and unavailable like status without changing parent mutation logic', async () => {
    const wrapper = mountPostCard();
    const likeAction = wrapper.findComponent(LikeAction);

    await wrapper.setProps({ post: { ...basePost(), likeStatus: 'unknown' } });
    expect(likeAction.props('loading')).toBe(true);
    expect(likeAction.props('ariaPressed')).toBe(null);

    await wrapper.setProps({ post: { ...basePost(), likeStatus: 'unavailable' } });
    expect(likeAction.props('disabled')).toBe(true);
    expect(likeAction.props('loading')).toBe(false);
    expect(likeAction.props('ariaPressed')).toBe(null);
  });

  it('forwards likePending as pending while preserving the optimistic visual props', () => {
    const wrapper = mount(PostCard, {
      props: { post: { ...basePost(), liked: true }, likePending: true },
      global: {
        stubs: {
          AuthorIdentity: { template: '<span class="author-identity" />' },
          LikeAction: {
            props: ['liked', 'count', 'disabled', 'loading', 'pending', 'ariaLabel', 'ariaPressed', 'variant'],
            template: '<button class="stub-like-action" type="button">{{ count }}</button>',
          },
          AppIcon: {
            props: ['name'],
            template: '<span class="test-icon" :data-icon="name" />',
          },
          RouterLink: RouterLinkStub,
        },
      },
    });
    const likeAction = wrapper.findComponent(LikeAction);

    expect(likeAction.props('pending')).toBe(true);
    expect(likeAction.props('liked')).toBe(true);
  });
});
