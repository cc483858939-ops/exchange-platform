// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, RouterLinkStub } from '@vue/test-utils';
import PostCard from './PostCard.vue';
import type { FeedPost } from '../../types/Feed';
import { formatCompactEngagementCount } from '../../utils/engagementCount';

const mocks = vi.hoisted(() => ({
  observeFeedCard: vi.fn(),
  unobserveFeedCard: vi.fn(),
  enqueue: vi.fn(),
}));

vi.mock('../../services/articleViewTelemetry', () => ({
  getArticleViewTelemetry: () => mocks,
}));

vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve: () => ({ href: '/news/42' }) }),
}));

const basePost = (): FeedPost => ({
  id: 42,
  author: {
    id: 7,
    username: 'reader',
    display_name: 'Reader',
    avatar_url: '',
  },
  title: 'Post',
  excerpt: 'Post body',
  coverImageUrl: '',
  createdAt: '2026-08-17T00:00:00.000Z',
  likeCount: 12,
  commentCount: 3,
  viewCount: 1234,
  liked: false,
  likeStatus: 'ready',
});

describe('PostCard View metric and telemetry lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  const mountPostCard = (post = basePost()) => mount(PostCard, {
    props: { post },
    global: {
      stubs: {
        AuthorIdentity: { template: '<span class="author-identity" />' },
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
      name: 'NewsDetail',
      params: { id: '42' },
    });
    expect(views?.text()).toContain(formatCompactEngagementCount(1234));
    expect(views?.attributes('aria-label')).toBe('Open post, 1,234 views');
    expect(wrapper.find('[data-icon="analytics"]').exists()).toBe(true);

    await views?.trigger('click');

    expect(wrapper.emitted('articleClick')).toEqual([[post]]);
    expect(wrapper.emitted('toggleLike') ?? []).toHaveLength(0);
    expect(wrapper.emitted('notInterested') ?? []).toHaveLength(0);
    expect(wrapper.emitted('deletePost') ?? []).toHaveLength(0);
    expect(mocks.enqueue).not.toHaveBeenCalled();
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
});
