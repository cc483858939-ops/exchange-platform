// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, nextTick, reactive } from 'vue';
import { createMemoryHistory, createRouter, RouterView } from 'vue-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  topicSession: null as any,
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/topicSession', () => ({ useTopicSessionStore: () => mocks.topicSession }));

import TopicView from './TopicView.vue';

const createDeferred = () => {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
};

const createSession = () => {
  const session = reactive({
    activeSlug: 'japan',
    topic: { slug: 'japan', label: 'Japan', description: '' } as null | { slug: string; label: string; description: string },
    items: [] as unknown[],
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
    setTopic: vi.fn(),
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
  });
  return session;
};

describe('TopicView navigation', () => {
  beforeEach(() => {
    mocks.authStore = reactive({ isAuthenticated: false, currentIdentity: null });
    mocks.topicSession = createSession();
  });

  it('commits a Topic route while the new Topic request is still pending', async () => {
    const aiRequest = createDeferred();
    let aiLoadPending = false;
    mocks.topicSession.setTopic = vi.fn((slug: string) => {
      mocks.topicSession.activeSlug = slug;
      if (slug !== 'ai') return Promise.resolve();

      aiLoadPending = true;
      mocks.topicSession.scrollTop = 0;
      mocks.topicSession.topic = null;
      mocks.topicSession.items = [];
      mocks.topicSession.loaded = false;
      mocks.topicSession.initialLoading = true;
      return aiRequest.promise.then(() => {
        aiLoadPending = false;
        mocks.topicSession.topic = { slug: 'ai', label: 'AI', description: '' };
        mocks.topicSession.loaded = true;
        mocks.topicSession.initialLoading = false;
      });
    });

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/topics/:slug', name: 'Topic', component: TopicView }],
    });
    await router.push('/topics/japan');
    await router.isReady();

    const wrapper = mount(defineComponent({ render: () => h(RouterView) }), {
      global: {
        plugins: [router],
        stubs: {
          AppIcon: true,
          MobileAccountMenu: true,
          PostCard: true,
        },
      },
    });
    await flushPromises();
    const viewport = wrapper.get('.topic-view__scroll').element as HTMLElement;
    viewport.scrollTop = 1200;

    let timeout: ReturnType<typeof setTimeout> | undefined;
    const navigation = router.push('/topics/ai');
    const result = await Promise.race([
      navigation.then(() => 'resolved' as const),
      new Promise<'pending'>(resolve => {
        timeout = setTimeout(() => resolve('pending'), 1000);
      }),
    ]);
    if (timeout !== undefined) clearTimeout(timeout);
    const committedBeforeLoadFinished = result === 'resolved';

    if (!committedBeforeLoadFinished) aiRequest.resolve();
    await navigation;
    await nextTick();
    await flushPromises();

    expect(committedBeforeLoadFinished).toBe(true);
    expect(router.currentRoute.value.fullPath).toBe('/topics/ai');
    expect(mocks.topicSession.setTopic).toHaveBeenLastCalledWith('ai');
    expect(aiLoadPending).toBe(true);
    expect(mocks.topicSession.initialLoading).toBe(true);
    expect(mocks.topicSession.scrollTop).toBe(0);
    expect(viewport.scrollTop).toBe(0);
    expect(mocks.topicSession.saveScrollTop).toHaveBeenCalledWith(0);
    expect(wrapper.text()).toContain('Loading topic posts...');

    aiRequest.resolve();
    await flushPromises();
    expect(aiLoadPending).toBe(false);
    expect(mocks.topicSession.loaded).toBe(true);
    wrapper.unmount();
  });
});
