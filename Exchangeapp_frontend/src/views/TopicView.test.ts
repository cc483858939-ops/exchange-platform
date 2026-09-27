// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, KeepAlive, reactive, ref } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  topicSession: null as any,
  route: null as any,
  router: null as any,
  beforeRouteLeave: null as null | (() => void),
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/topicSession', () => ({ useTopicSessionStore: () => mocks.topicSession }));
vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => mocks.route,
    useRouter: () => mocks.router,
    onBeforeRouteLeave: (guard: () => void) => { mocks.beforeRouteLeave = guard; },
  };
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
  scrollTop: 0,
  loadingMore: false,
  loadMoreError: '',
  viewerID: null as number | null,
  likePendingPostIDs: new Set<number>(),
  repostPendingPostIDs: new Set<number>(),
  bookmarkPendingPostIDs: new Set<number>(),
  mutationErrors: new Map<number, string>(),
  setTopic: vi.fn().mockResolvedValue(undefined),
  saveScrollTop: vi.fn((value: number) => {
    if (mocks.topicSession) mocks.topicSession.scrollTop = value;
  }),
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

const TopicDetailProbe = defineComponent({ template: '<main data-topic-detail />' });

const mountCachedTopic = () => {
  const topicVisible = ref(true);
  const wrapper = mount(defineComponent({
    setup: () => () => h(KeepAlive, null, {
      default: () => topicVisible.value
        ? h(TopicView, { key: 'topic' })
        : h(TopicDetailProbe, { key: 'detail' }),
    }),
  }), {
    global: {
      stubs: {
        AppIcon: true,
        MobileAccountMenu: true,
        PostCard: {
          props: ['post', 'trackView', 'requiresAuthForActions', 'viewSessionKey', 'likePending', 'repostPending', 'bookmarkPending'],
          template: '<article data-topic-card>{{ post.content }}</article>',
        },
      },
    },
  });
  return {
    wrapper,
    showTopic: (visible: boolean) => { topicVisible.value = visible; },
  };
};

describe('TopicView', () => {
  beforeEach(() => {
    mocks.authStore = reactive({ isAuthenticated: false, currentIdentity: null });
    mocks.route = reactive({ name: 'Topic', params: { slug: 'japan' }, fullPath: '/topics/japan' });
    mocks.router = { back: vi.fn(), push: vi.fn() };
    mocks.topicSession = createSession();
    mocks.beforeRouteLeave = null;
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

  it('loads the new Topic after the committed route changes and scrolls to the top', async () => {
    mocks.topicSession.setTopic = vi.fn((slug: string) => {
      mocks.topicSession.activeSlug = slug;
      return Promise.resolve();
    });
    const wrapper = mountTopic();
    const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;
    viewport.scrollTop = 80;

    mocks.route.params.slug = ' AI ';
    await flushPromises();

    expect(mocks.topicSession.setTopic).toHaveBeenLastCalledWith('ai');
    expect(viewport.scrollTop).toBe(0);
    expect(mocks.topicSession.saveScrollTop).toHaveBeenLastCalledWith(0);
  });

  it('ignores stale Topic completions after a newer slug becomes active', async () => {
    let resolveAI!: () => void;
    let resolveTechnology!: () => void;
    const aiRequest = new Promise<void>((resolve) => { resolveAI = resolve; });
    const technologyRequest = new Promise<void>((resolve) => { resolveTechnology = resolve; });
    const wrapper = mountTopic();
    const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;

    mocks.topicSession.setTopic = vi.fn((slug: string) => {
      mocks.topicSession.activeSlug = slug;
      if (slug === 'ai') return aiRequest;
      if (slug === 'technology') return technologyRequest;
      return Promise.resolve();
    });
    await flushPromises();

    mocks.route.params.slug = 'ai';
    await flushPromises();
    mocks.route.params.slug = 'technology';
    await flushPromises();

    resolveTechnology();
    await flushPromises();
    expect(mocks.topicSession.activeSlug).toBe('technology');
    expect(viewport.scrollTop).toBe(0);

    viewport.scrollTop = 47;
    resolveAI();
    await flushPromises();

    expect(viewport.scrollTop).toBe(47);
    expect(mocks.topicSession.saveScrollTop).toHaveBeenLastCalledWith(0);
    wrapper.unmount();
  });

  it('saves and restores the internal scroll position across activation without reloading', async () => {
    const { wrapper, showTopic } = mountCachedTopic();
    await flushPromises();
    const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;
    viewport.scrollTop = 1200;

    mocks.beforeRouteLeave?.();
    expect(mocks.topicSession.saveScrollTop).toHaveBeenCalledWith(1200);
    mocks.route.name = 'PostDetail';
    showTopic(false);
    await flushPromises();
    expect(wrapper.find('[data-topic-detail]').exists()).toBe(true);

    mocks.route.name = 'Topic';
    mocks.route.params.slug = 'japan';
    mocks.topicSession.scrollTop = 1200;
    showTopic(true);
    await flushPromises();

    expect(wrapper.get('.topic-view__scroll').element).toBe(viewport);
    expect(viewport.scrollTop).toBe(1200);
    expect(mocks.topicSession.setTopic).toHaveBeenCalledTimes(1);
  });

  it('disconnects the pagination observer while hidden and ignores its callback', async () => {
    const observers: Array<{
      callback: IntersectionObserverCallback;
      disconnect: ReturnType<typeof vi.fn>;
      observe: ReturnType<typeof vi.fn>;
    }> = [];
    class FakeIntersectionObserver {
      callback: IntersectionObserverCallback;
      disconnect = vi.fn();
      observe = vi.fn();

      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
        observers.push(this);
      }
    }
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
    mocks.topicSession = createSession({ nextCursor: 'cursor-1' });
    const { wrapper, showTopic } = mountCachedTopic();
    await flushPromises();
    const originalObserver = observers.at(-1);
    expect(originalObserver).toBeTruthy();

    mocks.route.name = 'PostDetail';
    showTopic(false);
    await flushPromises();

    expect(originalObserver?.disconnect).toHaveBeenCalled();
    originalObserver?.callback(
      [{ isIntersecting: true } as IntersectionObserverEntry],
      originalObserver as unknown as IntersectionObserver,
    );
    expect(mocks.topicSession.loadMore).not.toHaveBeenCalled();

    mocks.route.name = 'Topic';
    showTopic(true);
    await flushPromises();
    expect(observers.length).toBeGreaterThan(1);
    expect(observers.at(-1)?.observe).toHaveBeenCalled();
    wrapper.unmount();
  });
});
