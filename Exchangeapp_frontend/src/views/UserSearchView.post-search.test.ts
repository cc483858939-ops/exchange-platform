// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import type { Post } from '../types/Post';
import { usePostSearchSessionStore } from '../store/postSearchSession';
import { rememberedSearchRouteSnapshot } from '../router/searchRouteState';
import UserSearchView from './UserSearchView.vue';

const mocks = vi.hoisted(() => ({
  route: null as any,
  authStore: null as any,
  router: { push: vi.fn(), resolve: vi.fn() },
  searchPosts: vi.fn(),
  getPostEngagementStates: vi.fn(),
  searchUsers: vi.fn(),
  getUser: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
}));

vi.mock('vue-router', () => ({
  onBeforeRouteLeave: vi.fn(),
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));
vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../services/postSearchService', () => ({ searchPosts: mocks.searchPosts }));
vi.mock('../services/engagementService', () => ({ getPostEngagementStates: mocks.getPostEngagementStates }));
vi.mock('../services/userService', () => ({
  searchUsers: mocks.searchUsers,
  getUser: mocks.getUser,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));
vi.mock('../store/sessionSync', () => ({
  beginBookmarkStateMutation: vi.fn(),
  registerSearchSessionSync: vi.fn(),
  registerPostSearchSessionSync: vi.fn(),
  syncExternalFollowState: vi.fn(),
  syncPostSearchLikeState: vi.fn(),
  syncPostSearchRepostState: vi.fn(),
  syncPostSearchBookmarkState: vi.fn(),
}));

const post = (id: number): Post => ({
  id,
  created_at: '2026-09-30T12:00:00.000Z',
  updated_at: '2026-09-30T12:00:00.000Z',
  published_at: '2026-09-30T12:00:00.000Z',
  author: { id: 8, username: 'writer', display_name: 'Writer', avatar_url: '' },
  content: `Post ${id}`,
  language: 'en',
  conversation_id: id,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 0,
  repost_count: 0,
  reply_count: 0,
  quote_count: 0,
  view_count: 0,
  deleted: false,
});

const mountView = () => mount(UserSearchView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      RouterLink: { template: '<a><slot /></a>' },
      PostCard: {
        props: ['post', 'trackView', 'requiresAuthForActions'],
        emits: ['toggle-like', 'toggle-repost', 'toggle-bookmark'],
        template: '<article class="test-post" :data-id="post.id" :data-track-view="String(trackView)" :data-requires-auth="String(requiresAuthForActions)">{{ post.content }}<button class="test-like" @click="$emit(\'toggle-like\', post.id)">Like</button></article>',
      },
      UserRow: { template: '<div />' },
    },
  },
});

const mountViewWithRealPostCard = () => mount(UserSearchView, {
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      RouterLink: { props: ['to'], template: '<a><slot /></a>' },
      AuthorIdentity: { template: '<span class="test-author" />' },
      LinkifiedText: { props: ['text'], template: '<span>{{ text }}</span>' },
      PostMediaGrid: { template: '<div />' },
      ConfirmDialog: { template: '<div />' },
      LikeAction: {
        props: ['disabled', 'loading', 'pending'],
        emits: ['toggle'],
        template: '<button class="real-card-like" :disabled="disabled || loading || pending" @click="$emit(\'toggle\')">Like</button>',
      },
      RepostMenu: {
        props: ['disabled', 'loading', 'pending'],
        emits: ['toggle', 'quote', 'view-quotes'],
        template: '<div><button class="real-card-repost" :disabled="disabled || loading || pending" @click="$emit(\'toggle\')">Repost</button><button class="real-card-quote" @click="$emit(\'quote\')">Quote</button></div>',
      },
      BookmarkAction: {
        props: ['disabled', 'loading', 'pending'],
        emits: ['toggle'],
        template: '<button class="real-card-bookmark" :disabled="disabled || loading || pending" @click="$emit(\'toggle\')">Bookmark</button>',
      },
      UserRow: { template: '<div />' },
    },
  },
});

describe('UserSearchView Post Search', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.route = reactive({ name: 'UserSearch', query: { q: 'yen' }, fullPath: '/search?q=yen' });
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7, username: 'viewer' } });
    mocks.searchPosts.mockResolvedValue({ items: [post(1)], next_cursor: null });
    mocks.getPostEngagementStates.mockResolvedValue({ items: [] });
    mocks.router.resolve.mockImplementation((destination: { query?: { quote?: string } }) => ({
      fullPath: destination.query?.quote ? `/posts/new?quote=${destination.query.quote}` : '/posts/new',
    }));
    mocks.searchUsers.mockResolvedValue({ items: [], has_more: false });
  });

  it('defaults to Posts and renders canonical results without view tracking', async () => {
    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toBe('Posts');
    expect(wrapper.find('.test-post').attributes('data-track-view')).toBe('false');
    expect(wrapper.find('.test-post').attributes('data-requires-auth')).toBe('false');
    expect(mocks.searchPosts).toHaveBeenCalledWith(expect.objectContaining({ q: 'yen', sort: 'latest', limit: 20 }));
    wrapper.unmount();
  });

  it('connects PostCard engagement actions to the Post Search session', async () => {
    const wrapper = mountView();
    await flushPromises();
    const store = usePostSearchSessionStore();
    const toggleLike = vi.spyOn(store, 'toggleLike');

    await wrapper.get('.test-like').trigger('click');
    expect(toggleLike).toHaveBeenCalledWith(1);
    wrapper.unmount();
  });

  it('lets a real PostCard Like from Search reach the mutation without Login navigation', async () => {
    mocks.getPostEngagementStates.mockResolvedValue({ items: [{
      post_id: 1,
      like: { status: 'ready', likes: 0, liked: false },
      repost: { status: 'ready', reposts: 0, reposted: false },
      bookmark: { status: 'ready', bookmarked: false },
    }] });
    const wrapper = mountViewWithRealPostCard();
    await flushPromises();
    const store = usePostSearchSessionStore();
    const toggleLike = vi.spyOn(store, 'toggleLike').mockResolvedValue('succeeded');
    mocks.router.push.mockClear();

    await wrapper.get('.real-card-like').trigger('click');

    expect(toggleLike).toHaveBeenCalledWith(1);
    expect(mocks.router.push).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('opens Quote PostCreate directly from a real authenticated Search PostCard', async () => {
    const wrapper = mountViewWithRealPostCard();
    await flushPromises();
    mocks.router.push.mockClear();

    await wrapper.get('.real-card-quote').trigger('click');

    expect(mocks.router.push).toHaveBeenCalledWith({ name: 'PostCreate', query: { quote: '1' } });
    wrapper.unmount();
  });

  it('remembers canonical Post Search criteria without transient route state', async () => {
    const from = '2026-09-01T00:00:00.000Z';
    const to = '2026-09-02T00:00:00.000Z';
    mocks.route.query = { tab: 'posts', q: '日元', author: '42', time: 'custom', from, to, cursor: 'ignored' };
    const wrapper = mountView();
    await flushPromises();

    expect(rememberedSearchRouteSnapshot.value).toEqual({
      tab: 'posts', q: '日元', author: '42', time: 'custom', from, to,
    });
    wrapper.unmount();
  });

  it('uses an explicit People tab when switching while retaining the shared query', async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[role="tab"]:nth-child(2)').trigger('click');

    expect(mocks.router.push).toHaveBeenLastCalledWith({
      name: 'UserSearch',
      query: { q: 'yen', tab: 'people' },
    });
    wrapper.unmount();
  });

  it('shows the two-rune validation for Posts without sending a request', async () => {
    mocks.route.query = {};
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('input[aria-label="Search posts"]').setValue('日');
    await wrapper.get('.search-view__form').trigger('submit');

    expect(wrapper.text()).toContain('Enter at least 2 characters to search posts.');
    expect(mocks.searchPosts).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('debounces author suggestions and writes author id plus relative time to the route', async () => {
    vi.useFakeTimers();
    mocks.searchUsers.mockResolvedValue({
      items: [{ user: { id: 42, username: 'alice', display_name: 'Alice', avatar_url: '' }, following: false }],
      has_more: false,
    });
    const wrapper = mountView();
    await flushPromises();
    const authorInput = wrapper.get('#post-search-author');
    await authorInput.setValue('@alice');
    await vi.advanceTimersByTimeAsync(249);
    expect(mocks.searchUsers).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await flushPromises();
    expect(mocks.searchUsers).toHaveBeenCalledWith({ q: '@alice', limit: 8, offset: 0 });
    await wrapper.get('[role="option"]').trigger('click');
    await wrapper.get('#post-search-time').setValue('24h');
    await wrapper.get('.search-view__form').trigger('submit');

    expect(mocks.router.push).toHaveBeenLastCalledWith({
      name: 'UserSearch',
      query: { q: 'yen', tab: 'posts', author: '42', time: '24h' },
    });
    wrapper.unmount();
    vi.useRealTimers();
  });

  it('rehydrates an unavailable author by id and keeps that id until cleared', async () => {
    mocks.route.query = { tab: 'posts', q: 'yen', author: '42' };
    mocks.getUser.mockRejectedValue(new Error('unavailable'));
    const wrapper = mountView();
    await flushPromises();

    expect(mocks.getUser).toHaveBeenCalledWith(42);
    expect(mocks.searchPosts).toHaveBeenCalledWith(expect.objectContaining({ q: 'yen', author_id: 42 }));
    expect(wrapper.text()).toContain('Author unavailable');
    await wrapper.get('[aria-label="Clear author filter"]').trigger('click');
    await wrapper.get('.search-view__form').trigger('submit');

    expect(mocks.router.push).toHaveBeenLastCalledWith({
      name: 'UserSearch',
      query: { tab: 'posts', q: 'yen' },
    });
    wrapper.unmount();
  });

  it('serializes custom local dates as absolute ISO timestamps', async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('#post-search-time').setValue('custom');
    await wrapper.get('input[type="datetime-local"]').setValue('2026-09-01T08:00');
    await wrapper.findAll('input[type="datetime-local"]')[1].setValue('2026-09-02T08:00');
    await wrapper.get('.search-view__form').trigger('submit');

    const pushedQuery = mocks.router.push.mock.calls.at(-1)?.[0].query;
    expect(pushedQuery).toMatchObject({ tab: 'posts', q: 'yen', time: 'custom' });
    expect(pushedQuery.from).toBe(new Date('2026-09-01T08:00').toISOString());
    expect(pushedQuery.to).toBe(new Date('2026-09-02T08:00').toISOString());
    wrapper.unmount();
  });
});
