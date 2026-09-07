// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { useProfileSessionStore } from '../store/profileSession';
const mocks = vi.hoisted(() => ({
    route: { params: { id: '7' } },
    setRouteID: (_id) => { },
    getUser: vi.fn(),
    getUserPosts: vi.fn(),
    getUserFollowState: vi.fn(),
    followUser: vi.fn(),
    unfollowUser: vi.fn(),
    updateUserProfile: vi.fn(),
    uploadProfileAvatar: vi.fn(),
    deletePost: vi.fn(),
    getPostLikeStates: vi.fn(),
    likePost: vi.fn(),
    unlikePost: vi.fn(),
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
        isPostDeleted: vi.fn(),
        markPostDeleted: vi.fn(),
        replaceAuthorIdentity: vi.fn(),
        applyLikeStateUpdate: vi.fn(),
    },
}));
vi.mock('vue-router', async () => {
    const { reactive } = await import('vue');
    const route = reactive(mocks.route);
    mocks.setRouteID = (id) => {
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
    getUserPosts: mocks.getUserPosts,
    getUserFollowState: mocks.getUserFollowState,
    followUser: mocks.followUser,
    unfollowUser: mocks.unfollowUser,
    updateUserProfile: mocks.updateUserProfile,
    uploadProfileAvatar: mocks.uploadProfileAvatar,
}));
vi.mock('../services/postService', () => ({
    deletePost: mocks.deletePost,
}));
vi.mock('../services/likeService', () => ({
    getPostLikeStates: mocks.getPostLikeStates,
    likePost: mocks.likePost,
    unlikePost: mocks.unlikePost,
}));
class FakeIntersectionObserver {
    static instances = [];
    callback;
    observed = null;
    disconnectCount = 0;
    constructor(callback) {
        this.callback = callback;
        FakeIntersectionObserver.instances.push(this);
    }
    observe(element) {
        this.observed = element;
    }
    unobserve(_element) { }
    disconnect() {
        this.disconnectCount += 1;
    }
    takeRecords() {
        return [];
    }
    trigger(isIntersecting = true) {
        const entry = {
            isIntersecting,
            target: this.observed,
        };
        this.callback([entry], this);
    }
}
const originalIntersectionObserverDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'IntersectionObserver');
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
    }
    else {
        Reflect.deleteProperty(globalThis, 'IntersectionObserver');
    }
};
installFakeIntersectionObserver();
let UserProfileView;
const profile = (id) => ({
    id,
    username: `user-${id}`,
    display_name: `User ${id}`,
    avatar_url: '',
    bio: '',
    created_at: '2026-08-15T00:00:00.000Z',
});
const post = (id, authorID) => ({
    id,
    created_at: '2026-08-15T00:00:00.000Z',
    updated_at: '2026-08-15T00:00:00.000Z',
    published_at: '2026-08-15T00:00:00.000Z',
    author: {
        id: authorID,
        username: `user-${authorID}`,
        display_name: `User ${authorID}`,
        avatar_url: '',
    },
    content: `Body ${id}`,
    conversation_id: id,
    reply_to_post_id: null,
    quote_post_id: null,
    reply_to_post: null,
    quote_post: null,
    visibility: 'public',
    media: [],
    like_count: 0,
    reply_count: 0,
    view_count: 0,
    deleted: false,
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
const deferred = () => {
    let resolve;
    const promise = new Promise((promiseResolve) => {
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
const mountedViews = [];
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
        setActivePinia(createPinia());
        installFakeIntersectionObserver();
        vi.resetAllMocks();
        FakeIntersectionObserver.instances.length = 0;
        mocks.setRouteID('7');
        mocks.authStore.currentIdentity.id = 7;
        mocks.getUser.mockImplementation((id) => Promise.resolve(profile(Number(id))));
        mocks.getUserPosts.mockResolvedValue({ items: [], next_cursor: null });
        mocks.getUserFollowState.mockResolvedValue({
            following: false,
            follower_count: 0,
            following_count: 0,
        });
        mocks.getPostLikeStates.mockResolvedValue({ items: [], unavailable_post_ids: [] });
        mocks.deletePost.mockResolvedValue(undefined);
        mocks.feedStore.isPostDeleted.mockReturnValue(false);
        mocks.feedStore.markPostDeleted.mockReturnValue(true);
    });
    it('re-establishes the observer after delete and continues cursor pagination', async () => {
        mocks.getUserPosts
            .mockResolvedValueOnce({ items: [post(1, 7)], next_cursor: 'cursor-1' })
            .mockResolvedValueOnce({ items: [post(2, 7)], next_cursor: null });
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
        expect(mocks.getUserPosts).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
        expect(mounted.findAll('.post-card__id').map((node) => node.text())).toEqual(['2']);
        expect(mounted.findAll('.post-card__id')).toHaveLength(1);
        expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);
        expect(mounted.text()).not.toContain('Loading more posts...');
    });
    it('invalidates a pending load-more response without losing the original cursor', async () => {
        const pendingLoadMore = deferred();
        let serveNewPage = false;
        mocks.getUserPosts.mockImplementation((_id, options) => {
            if (!options?.cursor) {
                return Promise.resolve({ items: [post(1, 7)], next_cursor: 'cursor-1' });
            }
            return serveNewPage
                ? Promise.resolve({ items: [post(2, 7)], next_cursor: null })
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
        expect(mocks.getUserPosts).toHaveBeenNthCalledWith(2, '7', { limit: 20, cursor: 'cursor-1' });
        await mounted.find('.post-card__delete').trigger('click');
        await settle();
        expect(mounted.findAll('.post-card')).toHaveLength(0);
        expect(activeObserver()).toBeDefined();
        pendingLoadMore.resolve({ items: [post(2, 7)], next_cursor: 'cursor-2' });
        await settle();
        expect(mounted.findAll('.post-card')).toHaveLength(0);
        expect(mocks.getUserPosts).toHaveBeenCalledTimes(2);
        serveNewPage = true;
        const replacementObserver = activeObserver();
        expect(replacementObserver).toBeDefined();
        replacementObserver?.trigger();
        await settle();
        expect(mocks.getUserPosts).toHaveBeenNthCalledWith(3, '7', { limit: 20, cursor: 'cursor-1' });
        expect(mounted.findAll('.post-card__id').map((node) => node.text())).toEqual(['2']);
        expect(mounted.findAll('.post-card__id')).toHaveLength(1);
        expect(mounted.find('.profile-feed-sentinel').exists()).toBe(false);
        expect(mounted.text()).not.toContain('Loading more posts...');
    });
    it('restores cached scroll once and never rewinds it during pagination changes', async () => {
        const userAgentDescriptor = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent');
        Object.defineProperty(window.navigator, 'userAgent', {
            configurable: true,
            value: 'Mozilla/5.0',
        });
        const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => { });
        const profileStore = useProfileSessionStore();
        const session = profileStore.ensureSession(7);
        session.user = profile(7);
        session.profileLoaded = true;
        session.postsLoaded = true;
        session.posts = [post(1, 7)];
        session.loadedPostIds.add(1);
        session.hasMore = true;
        session.nextCursor = 'cursor-1';
        session.scrollY = 500;
        const mounted = mountProfile();
        mountedViews.push(mounted);
        await settle();
        expect(scrollTo).toHaveBeenCalledTimes(1);
        expect(scrollTo).toHaveBeenCalledWith({ top: 500, behavior: 'auto' });
        session.postsLoadingMore = true;
        session.posts = [...session.posts, post(2, 7)];
        session.postsLoadingMore = false;
        await settle();
        expect(scrollTo).toHaveBeenCalledTimes(1);
        if (userAgentDescriptor) {
            Object.defineProperty(window.navigator, 'userAgent', userAgentDescriptor);
        }
        scrollTo.mockRestore();
    });
    it('saves the previous profile and restores the next profile once on route switch', async () => {
        const userAgentDescriptor = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent');
        Object.defineProperty(window.navigator, 'userAgent', {
            configurable: true,
            value: 'Mozilla/5.0',
        });
        Object.defineProperty(window, 'scrollY', {
            configurable: true,
            writable: true,
            value: 400,
        });
        const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => { });
        const profileStore = useProfileSessionStore();
        const first = profileStore.ensureSession(7);
        first.user = profile(7);
        first.profileLoaded = true;
        first.postsLoaded = true;
        first.scrollY = 400;
        const second = profileStore.ensureSession(8);
        second.user = profile(8);
        second.profileLoaded = true;
        second.postsLoaded = true;
        second.scrollY = 900;
        const mounted = mountProfile();
        mountedViews.push(mounted);
        await settle();
        expect(scrollTo).toHaveBeenLastCalledWith({ top: 400, behavior: 'auto' });
        mocks.setRouteID('8');
        await settle();
        expect(scrollTo).toHaveBeenLastCalledWith({ top: 900, behavior: 'auto' });
        expect(scrollTo).toHaveBeenCalledTimes(2);
        expect(first.scrollY).toBe(400);
        if (userAgentDescriptor) {
            Object.defineProperty(window.navigator, 'userAgent', userAgentDescriptor);
        }
        scrollTo.mockRestore();
    });
});
