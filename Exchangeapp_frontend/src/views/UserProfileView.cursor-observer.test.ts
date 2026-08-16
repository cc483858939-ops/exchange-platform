// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

type Deferred<T> = {
  promise: Promise<T>;
  resolve: (value: T) => void;
};

const mocks = vi.hoisted(() => ({
  route: { params: { id: '7' } },
  setRouteID: (_id: string) => {},
  getUser: vi.fn(),
  getUserArticles: vi.fn(),
  getUserFollowState: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  updateUserProfile: vi.fn(),
  uploadProfileAvatar: vi.fn(),
  deleteArticle: vi.fn(),
  getArticleLikeStates: vi.fn(),
  likeArticle: vi.fn(),
  unlikeArticle: vi.fn(),
  router: {
    back: vi.fn(),
    push: vi.fn(),
  },
  authStore: {
    isAuthenticated: true,
    currentIdentity: {
      id: 7,
      username: 'viewer',
      display_name: 'Viewer',
      avatar_url: '',
    },
  },
  feedStore: {
    isArticleDeleted: vi.fn(),
    markArticleDeleted: vi.fn(),
    replaceAuthorIdentity: vi.fn(),
  },
}));

vi.mock('vue-router', async () => {
  const { reactive } = await import('vue');
  const route = reactive(mocks.route);
  mocks.setRouteID = (id: string) => {
    route.params.id = id;
  };
  return {
    useRoute: () => route,
    useRouter: () => mocks.router,
  };
});

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

vi.mock('../store/feed', () => ({
  useFeedStore: () => mocks.feedStore,
}));

vi.mock('../services/userService', () => ({
  getUser: mocks.getUser,
  getUserArticles: mocks.getUserArticles,
  getUserFollowState: mocks.getUserFollowState,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
  updateUserProfile: mocks.updateUserProfile,
  uploadProfileAvatar: mocks.uploadProfileAvatar,
}));

vi.mock('../services/articleService', () => ({
  deleteArticle: mocks.deleteArticle,
}));

vi.mock('../services/likeService', () => ({
  getArticleLikeStates: mocks.getArticleLikeStates,
  likeArticle: mocks.likeArticle,
  unlikeArticle: mocks.unlikeArticle,
}));

class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = [];

  readonly callback: IntersectionObserverCallback;
  observed: Element | null = null;
  disconnectCount = 0;

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback;
    FakeIntersectionObserver.instances.push(this);
  }

  observe(element: Element) {
    this.observed = element;
  }

  unobserve(_element: Element) {}

  disconnect() {
    this.disconnectCount += 1;
  }

  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }

  trigger(isIntersecting = true) {
    const entry = {
      isIntersecting,
      target: this.observed,
    } as IntersectionObserverEntry;
    this.callback([entry], this as unknown as IntersectionObserver);
  }
}

const originalIntersectionObserverDescriptor = Object.getOwnPropertyDescriptor(
  globalThis,
  'IntersectionObserver',
);
const installFakeIntersectionObserver = () => {
  Object.defineProperty(globalThis, 'IntersectionObserver', {
    configurable: true,
    writable: true,
    value: FakeIntersectionObserver,
  });
};
const restoreIntersectionObserver = () => {
  if (originalIntersectionObserverDescriptor) {
    Object.defineProperty(globalThis, 'IntersectionObserver', originalIntersectionObserverDescriptor);
  } else {
    Reflect.deleteProperty(globalThis, 'IntersectionObserver');
  }
};
installFakeIntersectionObserver();

type UserProfileComponent = typeof import('./UserProfileView.vue')['default'];
let UserProfileView: UserProfileComponent;

const profile = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: '',
  created_at: '2026-08-15T00:00:00.000Z',
});

const article = (id: number, authorID: number) => ({
  ID: id,
  CreatedAt: '2026-08-15T00:00:00.000Z',
  UpdatedAt: '2026-08-15T00:00:00.000Z',
  title: `Post ${id}`,
  content: `Body ${id}`,
  preview: `Preview ${id}`,
  cover_image_url: '',
  summary: '',
  tags: [],
  category: 'News',
  publication_state: 'published',
  analysis_state: 'pending',
  analysis_version: 'v1',
  published_at: '2026-08-15T00:00:00.000Z',
  expired_at: null,
  like_count: 0,
  comment_count: 0,
  like_sync_version: 0,
  author: {
    id: authorID,
    username: `user-${authorID}`,
    display_name: `User ${authorID}`,
    avatar_url: '',
  },
});

const PostCardStub = {
  props: ['post', 'showDelete'],
  template: `
    <article class="post-card">
      <span class="post-card__id">{{ post.id }}</span>
      <button v-if="showDelete" class="post-card__delete" type="button" @click="$emit('delete-post', post.id)">Delete</button>
    </article>
  `,
};

const settle = async () => {
  await flushPromises();
  await nextTick();
  await flushPromises();
};

const deferred = <T>(): Deferred<T> => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
};

const mountProfile = () => mount(UserProfileView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      PostCard: PostCardStub,
      RouterLink: { template: '<a><slot /></a>' },
    },
  },
});

const activeObserver = () => [...FakeIntersectionObserver.instances]
  .reverse()
  .find((candidate) => candidate.observed !== null && candidate.disconnectCount === 0);

const mountedViews: ReturnType<typeof mountProfile>[] = [];

beforeAll(async () => {
  vi.resetModules();
  UserProfileView = (await import('./UserProfileView.vue')).default;
});

afterEach(() => {
  mountedViews.splice(0).forEach((mounted) => mounted.unmount());
  FakeIntersectionObserver.instances.length = 0;
  vi.clearAllMocks();
  restoreIntersectionObserver();
});

afterAll(() => {
  restoreIntersectionObserver();
});

describe('UserProfileView observer and cursor concurrency', () => {
  beforeEach(() => {
    installFakeIntersectionObserver();
    vi.resetAllMocks();
    FakeIntersectionObserver.instances.length = 0;
    mocks.setRouteID('7');
    mocks.authStore.currentIdentity.id = 7;
    mocks.getUser.mockImplementation((id: string) => Promise.resolve(profile(Number(id))));
    mocks.getUserArticles.mockResolvedValue({ items: [], next_cursor: null });
    mocks.getUserFollowState.mockResolvedValue({
      following: false,
      follower_count: 0,
      following_count: 0,
    });
    mocks.getArticleLikeStates.mockResolvedValue({ items: [], unavailable_article_ids: [] });
    mocks.deleteArticle.mockResolvedValue(undefined);
    mocks.feedStore.isArticleDeleted.mockReturnValue(false);
    mocks.feedStore.markArticleDeleted.mockReturnValue(true);
  });

  it('re-establishes the observer after delete and continues cursor pagination', async () => {
    mocks.getUserArticles
      .mockResolvedValueOnce({ items: [article(1, 7)], next_cursor: 'cursor-1' })
      .mockResolvedValueOnce({ items: [article(2, 7)], next_cursor: null });

    const mounted = mountProfile();
    mountedViews.push(mounted);
    await settle();

    const initialObserver = activeObserver();
    expect(initialObserver).toBeDefined();
    expect(initialObserver?.observed).toBe(mounted.find('.profile-feed-sentinel').element);

    await mounted.find('.post-card__delete').trigger('click');
    await settle();

    expect(mounted.findAll('.post-card')).toHaveLength(0);
    expect(initialObserver?.disconnectCount).toBeGreaterThan(0);

    const replacementObserver = activeObserver();
    expect(replacementObserver).toBeDefined();
    expect(replacementObserver).not.toBe(initialObserver);
    expect(replacementObserver?.observed).toBe(mounted.find('.profile-feed-sentinel').element);

    replacementObserver?.trigger();
    await settle();

    expect(mocks.getUserArticles).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
    expect(mounted.findAll('.post-card__id').map((node) => node.text())).toEqual(['2']);
    expect(mounted.findAll('.post-card__id')).toHaveLength(1);
    expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);
    expect(mounted.text()).not.toContain('Loading more posts...');
  });

  it('invalidates a pending load-more response without losing the original cursor', async () => {
    const pendingLoadMore = deferred<{ items: ReturnType<typeof article>[]; next_cursor: string | null }>();
    let serveNewPage = false;
    mocks.getUserArticles.mockImplementation((_id: string, options?: { cursor?: string }) => {
      if (!options?.cursor) {
        return Promise.resolve({ items: [article(1, 7)], next_cursor: 'cursor-1' });
      }
      return serveNewPage
        ? Promise.resolve({ items: [article(2, 7)], next_cursor: null })
        : pendingLoadMore.promise;
    });

    const mounted = mountProfile();
    mountedViews.push(mounted);
    await settle();

    const initialObserver = activeObserver();
    expect(initialObserver).toBeDefined();
    initialObserver?.trigger();
    await nextTick();
    await flushPromises();

    expect(mocks.getUserArticles).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });

    await mounted.find('.post-card__delete').trigger('click');
    await settle();

    expect(mounted.findAll('.post-card')).toHaveLength(0);
    expect(activeObserver()).toBeDefined();

    pendingLoadMore.resolve({ items: [article(2, 7)], next_cursor: 'cursor-2' });
    await settle();

    expect(mounted.findAll('.post-card')).toHaveLength(0);
    expect(mocks.getUserArticles).toHaveBeenCalledTimes(2);

    serveNewPage = true;
    const replacementObserver = activeObserver();
    expect(replacementObserver).toBeDefined();
    replacementObserver?.trigger();
    await settle();

    expect(mocks.getUserArticles).toHaveBeenNthCalledWith(3, '7', { limit: 20, cursor: 'cursor-1' });
    expect(mounted.findAll('.post-card__id').map((node) => node.text())).toEqual(['2']);
    expect(mounted.findAll('.post-card__id')).toHaveLength(1);
    expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);
    expect(mounted.text()).not.toContain('Loading more posts...');
  });
});
