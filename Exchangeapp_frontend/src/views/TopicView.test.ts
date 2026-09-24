// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  topicSession: null as any,
  route: null as any,
  router: null as any,
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/topicSession', () => ({ useTopicSessionStore: () => mocks.topicSession }));
vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return { ...actual, useRoute: () => mocks.route, useRouter: () => mocks.router };
});

import TopicView from './TopicView.vue';

const feedPost = {
  id: 1,
  content: 'Topic body',
  author: { id: 9, username: 'author', display_name: 'Author', avatar_url: '' },
  media: [],
  createdAt: '2026-09-20T12:00:00.000Z',
  likeCount: 3,
  replyCount: 1,
  viewCount: 0,
  liked: false,
  likeStatus: 'ready',
  repostCount: 0,
  reposted: false,
  repostStatus: 'ready',
  bookmarked: false,
  bookmarkStatus: 'ready',
  language: 'und',
};

const createSession = (overrides: Record<string, unknown> = {}) => reactive({
  activeSlug: 'japan',
  topic: { slug: 'japan', label: 'Japan', description: 'Life, culture & places in Japan' },
  items: [feedPost],
  loaded: true,
  initialLoading: false,
  initialError: '',
  nextCursor: null as string | null,
  loadingMore: false,
  loadMoreError: '',
  viewerID: null as number | null,
  likePendingPostIDs: new Set<number>(),
  repostPendingPostIDs: new Set<number>(),
  bookmarkPendingPostIDs: new Set<number>(),
  mutationErrors: new Map<number, string>(),
  setTopic: vi.fn().mockResolvedValue(undefined),
  reset: vi.fn(),
  retryInitial: vi.fn(),
  retryLoadMore: vi.fn(),
  loadMore: vi.fn(),
  toggleLike: vi.fn(),
  toggleRepost: vi.fn(),
  toggleBookmark: vi.fn(),
  ...overrides,
});

const mountTopic = () => mount(TopicView, {
  global: {
    stubs: {
      AppIcon: true,
      MobileAccountMenu: true,
      PostCard: {
        props: ['post', 'trackView', 'requiresAuthForActions', 'viewSessionKey', 'likePending', 'repostPending', 'bookmarkPending'],
        template: '<article data-topic-card :data-track-view="trackView" :data-requires-auth="requiresAuthForActions" :data-session-key="viewSessionKey">{{ post.content }}</article>',
      },
    },
  },
});

describe('TopicView', () => {
  beforeEach(() => {
    mocks.authStore = reactive({ isAuthenticated: false, currentIdentity: null });
    mocks.route = reactive({ name: 'Topic', params: { slug: 'japan' }, fullPath: '/topics/japan' });
    mocks.router = { back: vi.fn(), push: vi.fn() };
    mocks.topicSession = createSession();
  });

  afterEach(() => vi.unstubAllGlobals());

  it('renders the topic header and guest PostCards without view telemetry', async () => {
    const wrapper = mountTopic();
    await flushPromises();

    expect(wrapper.get('h1').text()).toBe('#Japan');
    expect(wrapper.text()).toContain('Life, culture & places in Japan');
    const card = wrapper.get('[data-topic-card]');
    expect(card.text()).toBe('Topic body');
    expect(card.attributes('data-track-view')).toBe('false');
    expect(card.attributes('data-requires-auth')).toBe('true');
    expect(card.attributes('data-session-key')).toBe('topic:anonymous:japan');
  });

  it('shows loading, empty and retry states', async () => {
    mocks.topicSession = createSession({ initialLoading: true, loaded: false, items: [] });
    const loading = mountTopic();
    expect(loading.text()).toContain('Loading topic posts...');
    loading.unmount();

    mocks.topicSession = createSession({ loaded: true, items: [] });
    const empty = mountTopic();
    expect(empty.text()).toContain('There are no posts in this topic right now.');
    empty.unmount();

    mocks.topicSession = createSession({ initialError: 'Topic posts could not be loaded.', loaded: false, items: [] });
    const failed = mountTopic();
    await failed.get('button.topic-view__primary').trigger('click');
    expect(mocks.topicSession.retryInitial).toHaveBeenCalledOnce();
  });

  it('offers load-more retry and button fallback when IntersectionObserver is unavailable', async () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    mocks.topicSession = createSession({ nextCursor: 'cursor', loadMoreError: 'Could not load more posts.' });
    const wrapper = mountTopic();

    expect(wrapper.text()).toContain('Could not load more posts.');
    await wrapper.get('button.topic-view__primary').trigger('click');
    expect(mocks.topicSession.retryLoadMore).toHaveBeenCalledOnce();
    wrapper.unmount();

    mocks.topicSession = createSession({ nextCursor: 'cursor' });
    const fallback = mountTopic();
    expect(fallback.text()).toContain('Load more posts');
    await fallback.get('button.topic-view__primary').trigger('click');
    expect(mocks.topicSession.loadMore).toHaveBeenCalledOnce();
  });

  it('loads a reused route with the new slug and scrolls its feed to the top', async () => {
    const wrapper = mountTopic();
    const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;
    viewport.scrollTop = 80;
    mocks.route.params.slug = 'ai';
    await flushPromises();

    expect(mocks.topicSession.setTopic).toHaveBeenLastCalledWith('ai');
    expect(viewport.scrollTop).toBe(0);
  });
});
