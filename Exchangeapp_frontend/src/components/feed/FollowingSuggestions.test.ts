// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { defineComponent, h, KeepAlive, nextTick, reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import FollowingSuggestions from './FollowingSuggestions.vue';

const mocks = vi.hoisted(() => ({
  authStore: null as any,
  homeTimeline: null as any,
  getSuggestedUsers: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  syncExternalFollowState: vi.fn(),
}));

vi.mock('../../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../../store/homeTimeline', () => ({ useHomeTimelineStore: () => mocks.homeTimeline }));
vi.mock('../../store/sessionSync', () => ({ syncExternalFollowState: mocks.syncExternalFollowState }));
vi.mock('../../services/userService', () => ({
  getSuggestedUsers: mocks.getSuggestedUsers,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));

const makeUser = (id: number) => ({
  id,
  username: `user-${id}`,
  display_name: `User ${id}`,
  avatar_url: '',
  bio: `Bio ${id}`,
  created_at: '2026-09-01T00:00:00.000Z',
});

const connection = (id: number, following = false) => ({ user: makeUser(id), following });
const page = (items: ReturnType<typeof connection>[]) => ({ items, has_more: false });
const followResponse = (userId: number, following: boolean) => ({
  user_id: userId,
  following,
  follower_count: following ? 1 : 0,
  following_count: 0,
});

const UserRowStub = defineComponent({
  props: ['item', 'pending', 'error', 'isSelf'],
  emits: ['toggle-follow'],
  template: `<article class="user-row">
    <span>{{ item.user.username }}</span>
    <button class="test-follow" :data-user="item.user.id" :data-following="item.following" :disabled="pending" @click="$emit('toggle-follow', item.user.id)">{{ item.following ? 'Following' : 'Follow' }}</button>
    <p>{{ error }}</p>
  </article>`,
});

const mountSuggestions = (viewerId = 7) => mount(FollowingSuggestions, {
  props: { viewerId },
  global: {
    stubs: {
      AppIcon: { template: '<span />' },
      UserRow: UserRowStub,
      RouterLink: defineComponent({
        props: ['to'],
        template: '<a :data-route-name="to.name" :data-route-tab="to.query?.tab"><slot /></a>',
      }),
    },
  },
});

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};

describe('FollowingSuggestions', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: makeUser(7) });
    mocks.homeTimeline = { revalidateFollowing: vi.fn().mockResolvedValue(undefined) };
    mocks.getSuggestedUsers.mockResolvedValue(page([connection(8)]));
    mocks.followUser.mockResolvedValue(followResponse(8, true));
    mocks.unfollowUser.mockResolvedValue(followResponse(8, false));
  });

  it('renders at most three suggestions and preserves the discovery actions', async () => {
    mocks.getSuggestedUsers.mockResolvedValueOnce(page([
      connection(8), connection(9), connection(10), connection(11),
    ]));
    const wrapper = mountSuggestions();
    await flushPromises();

    expect(mocks.getSuggestedUsers).toHaveBeenCalledOnce();
    expect(mocks.getSuggestedUsers).toHaveBeenCalledWith(3);
    expect(wrapper.text()).toContain('Discover people to follow and see their posts here.');
    expect(wrapper.get('.following-suggestions__title').text()).toBe('Suggested for you');
    expect(wrapper.findAll('.user-row')).toHaveLength(3);
    expect(wrapper.get('.following-suggestions__primary').text()).toBe('Find more people');
    expect(wrapper.get('.following-suggestions__primary').attributes('data-route-name')).toBe('UserSearch');
    expect(wrapper.get('.following-suggestions__primary').attributes('data-route-tab')).toBe('people');
    expect(wrapper.get('.following-suggestions__links a').attributes('data-route-name')).toBe('TopicDirectory');

    await wrapper.get('button.following-suggestions__link').trigger('click');
    expect(wrapper.emitted('browse-for-you')).toHaveLength(1);
    wrapper.unmount();
  });

  it('restores the V1 empty state when the response is empty or unavailable', async () => {
    mocks.getSuggestedUsers.mockResolvedValueOnce(page([]));
    const empty = mountSuggestions();
    await flushPromises();
    expect(empty.text()).toContain('Find people to follow and see their posts here.');
    expect(empty.find('.following-suggestions__title').exists()).toBe(false);
    expect(empty.get('.following-suggestions__primary').text()).toBe('Find people');
    empty.unmount();

    mocks.getSuggestedUsers.mockRejectedValueOnce(new Error('offline'));
    const failed = mountSuggestions();
    await flushPromises();
    expect(failed.text()).toContain('Find people to follow and see their posts here.');
    expect(failed.find('.following-suggestions__content').exists()).toBe(false);
    expect(failed.get('.following-suggestions__primary').text()).toBe('Find people');
    failed.unmount();
  });

  it('does not request recommendations without an active authenticated viewer', async () => {
    mocks.authStore.isAuthenticated = false;
    const wrapper = mountSuggestions(7);
    await flushPromises();
    expect(mocks.getSuggestedUsers).not.toHaveBeenCalled();
    expect(wrapper.get('.following-suggestions__primary').text()).toBe('Find people');
    wrapper.unmount();
  });

  it('prevents duplicate follows, syncs success, and revalidates Following', async () => {
    const pendingFollow = deferred<ReturnType<typeof followResponse>>();
    mocks.followUser.mockReturnValueOnce(pendingFollow.promise);
    const wrapper = mountSuggestions();
    await flushPromises();

    const button = wrapper.get('.test-follow');
    await button.trigger('click');
    expect(button.attributes('disabled')).toBeDefined();
    await button.trigger('click');
    expect(mocks.followUser).toHaveBeenCalledTimes(1);

    pendingFollow.resolve(followResponse(8, true));
    await flushPromises();
    expect(wrapper.get('.test-follow').attributes('data-following')).toBe('true');
    expect(mocks.syncExternalFollowState).toHaveBeenCalledWith(followResponse(8, true));
    expect(mocks.homeTimeline.revalidateFollowing).toHaveBeenCalledOnce();
    wrapper.unmount();
  });

  it('shows an inline follow error and allows a later retry', async () => {
    mocks.followUser.mockRejectedValueOnce(new Error('offline'));
    const wrapper = mountSuggestions();
    await flushPromises();

    await wrapper.get('.test-follow').trigger('click');
    await flushPromises();
    expect(wrapper.get('.user-row p').text()).toBe('Could not update follow status.');
    expect(wrapper.get('.test-follow').attributes('data-following')).toBe('false');
    expect(wrapper.get('.test-follow').attributes('disabled')).toBeUndefined();
    expect(mocks.syncExternalFollowState).not.toHaveBeenCalled();

    await wrapper.get('.test-follow').trigger('click');
    await flushPromises();
    expect(wrapper.get('.test-follow').attributes('data-following')).toBe('true');
    expect(wrapper.get('.user-row p').text()).toBe('');
    wrapper.unmount();
  });

  it('drops stale results after switching viewer identity', async () => {
    const stale = deferred<ReturnType<typeof page>>();
    mocks.getSuggestedUsers
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce(page([connection(18)]));
    const wrapper = mountSuggestions();
    mocks.authStore.currentIdentity = makeUser(17);
    await wrapper.setProps({ viewerId: 17 });
    await flushPromises();

    stale.resolve(page([connection(8)]));
    await flushPromises();
    expect(mocks.getSuggestedUsers).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain('user-18');
    expect(wrapper.text()).not.toContain('user-8');
    wrapper.unmount();
  });

  it('does not sync a pending follow after the viewer identity changes', async () => {
    const pendingFollow = deferred<ReturnType<typeof followResponse>>();
    mocks.followUser.mockReturnValueOnce(pendingFollow.promise);
    mocks.getSuggestedUsers
      .mockResolvedValueOnce(page([connection(8)]))
      .mockResolvedValueOnce(page([connection(18)]));
    const wrapper = mountSuggestions();
    await flushPromises();
    await wrapper.get('.test-follow').trigger('click');

    mocks.authStore.currentIdentity = makeUser(17);
    await wrapper.setProps({ viewerId: 17 });
    await flushPromises();
    pendingFollow.resolve(followResponse(8, true));
    await flushPromises();

    expect(wrapper.text()).toContain('user-18');
    expect(wrapper.text()).not.toContain('user-8');
    expect(mocks.syncExternalFollowState).not.toHaveBeenCalled();
    expect(mocks.homeTimeline.revalidateFollowing).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('invalidates a response while KeepAlive deactivates and reloads on return', async () => {
    const stale = deferred<ReturnType<typeof page>>();
    mocks.getSuggestedUsers
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce(page([connection(18)]));
    const state = reactive({ active: true });
    const Placeholder = defineComponent({ template: '<div />' });
    const Host = defineComponent({
      setup() {
        return () => h(KeepAlive, {}, {
          default: () => state.active
            ? h(FollowingSuggestions, { viewerId: 7 })
            : h(Placeholder),
        });
      },
    });
    const wrapper = mount(Host, {
      global: {
        stubs: {
          AppIcon: { template: '<span />' },
          UserRow: UserRowStub,
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    });
    await nextTick();
    state.active = false;
    await nextTick();
    stale.resolve(page([connection(8)]));
    await flushPromises();
    state.active = true;
    await nextTick();
    await flushPromises();

    expect(mocks.getSuggestedUsers).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain('user-18');
    expect(wrapper.text()).not.toContain('user-8');
    wrapper.unmount();
  });

  it('applies a pending follow to the refreshed row after KeepAlive reactivation', async () => {
    const pendingFollow = deferred<ReturnType<typeof followResponse>>();
    mocks.getSuggestedUsers
      .mockResolvedValueOnce(page([connection(8)]))
      .mockResolvedValueOnce(page([connection(8)]));
    mocks.followUser.mockReturnValueOnce(pendingFollow.promise);
    const state = reactive({ active: true });
    const Placeholder = defineComponent({ template: '<div />' });
    const Host = defineComponent({
      setup() {
        return () => h(KeepAlive, {}, {
          default: () => state.active
            ? h(FollowingSuggestions, { viewerId: 7 })
            : h(Placeholder),
        });
      },
    });
    const wrapper = mount(Host, {
      global: {
        stubs: {
          AppIcon: { template: '<span />' },
          UserRow: UserRowStub,
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    });
    await flushPromises();
    await wrapper.get('.test-follow').trigger('click');
    state.active = false;
    await nextTick();
    state.active = true;
    await nextTick();
    await flushPromises();
    expect(wrapper.get('.test-follow').attributes('disabled')).toBeDefined();

    pendingFollow.resolve(followResponse(8, true));
    await flushPromises();
    expect(wrapper.get('.test-follow').attributes('data-following')).toBe('true');
    expect(mocks.syncExternalFollowState).toHaveBeenCalledWith(followResponse(8, true));
    expect(mocks.homeTimeline.revalidateFollowing).toHaveBeenCalledOnce();
    wrapper.unmount();
  });
});
