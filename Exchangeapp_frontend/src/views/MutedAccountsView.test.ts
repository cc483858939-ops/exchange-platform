// @vitest-environment jsdom

import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { reactive } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import MutedAccountsView from './MutedAccountsView.vue';

const mocks = vi.hoisted(() => ({
  authStore: null as {
    isAuthenticated: boolean;
    currentIdentity: { id: number } | null;
  } | null,
  back: vi.fn(),
  push: vi.fn(),
  getMyMutedUsers: vi.fn(),
  muteUser: vi.fn(),
  unmuteUser: vi.fn(),
  showUserMuteError: vi.fn(),
  showUserUnmuteSuccess: vi.fn(),
  homeTimeline: { setViewer: vi.fn(), applyMutedAuthorStateLocal: vi.fn() },
  notifications: { setViewer: vi.fn(), applyMutedActorStateLocal: vi.fn() },
}));

vi.mock('../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../store/homeTimeline', () => ({ useHomeTimelineStore: () => mocks.homeTimeline }));
vi.mock('../store/notification', () => ({ useNotificationStore: () => mocks.notifications }));
vi.mock('../services/userMuteService', () => ({
  getMyMutedUsers: mocks.getMyMutedUsers,
  muteUser: mocks.muteUser,
  unmuteUser: mocks.unmuteUser,
}));
vi.mock('../utils/userMuteFeedback', () => ({
  showUserMuteError: mocks.showUserMuteError,
  showUserUnmuteSuccess: mocks.showUserUnmuteSuccess,
}));
vi.mock('vue-router', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => ({ fullPath: '/settings/muted' }),
    useRouter: () => ({ back: mocks.back, push: mocks.push }),
  };
});

import { useUserMuteStore } from '../store/userMute';

const mutedUser = (id: number, username: string) => ({
  user: { id, username, display_name: username.toUpperCase(), avatar_url: '' },
  created_at: '2026-10-14T00:00:00Z',
});

const mountView = (pinia = createPinia()) => mount(MutedAccountsView, {
  global: {
    plugins: [pinia],
    stubs: {
      AppIcon: { props: ['name'], template: '<span :data-icon="name" />' },
      MobileAccountMenu: true,
      UserAvatar: { props: ['username'], template: '<span class="user-avatar-stub">{{ username }}</span>' },
      RouterLink: RouterLinkStub,
    },
  },
});

describe('MutedAccountsView', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    mocks.authStore = reactive({ isAuthenticated: true, currentIdentity: { id: 7 } });
    mocks.back.mockReset();
    mocks.push.mockReset();
    mocks.getMyMutedUsers.mockReset().mockResolvedValue({ items: [], next_cursor: null });
    mocks.muteUser.mockReset().mockResolvedValue(undefined);
    mocks.unmuteUser.mockReset().mockResolvedValue(undefined);
    mocks.showUserMuteError.mockReset();
    mocks.showUserUnmuteSuccess.mockReset();
    mocks.homeTimeline.setViewer.mockReset();
    mocks.homeTimeline.applyMutedAuthorStateLocal.mockReset();
    mocks.notifications.setViewer.mockReset();
    mocks.notifications.applyMutedActorStateLocal.mockReset();
  });

  it('loads pages, opens profiles, and refreshes immediately after unmuting', async () => {
    mocks.getMyMutedUsers
      .mockResolvedValueOnce({ items: [mutedUser(8, 'alice')], next_cursor: 'next' })
      .mockResolvedValueOnce({ items: [mutedUser(9, 'bob')], next_cursor: null })
      .mockResolvedValueOnce({ items: [mutedUser(9, 'bob')], next_cursor: null });
    const pinia = createPinia();
    const wrapper = mountView(pinia);
    await flushPromises();

    expect(wrapper.text()).toContain('Muted accounts');
    expect(wrapper.text()).toContain('ALICE');
    const profile = wrapper.findAllComponents(RouterLinkStub)
      .find(link => (link.props('to') as { name?: string }).name === 'UserProfile');
    expect(profile?.props('to')).toEqual({ name: 'UserProfile', params: { id: '8' } });

    await wrapper.get('.muted-accounts-view__load-more').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('BOB');

    await wrapper.get('.muted-account__unmute').trigger('click');
    await flushPromises();
    expect(mocks.unmuteUser).toHaveBeenCalledExactlyOnceWith(8);
    expect(mocks.showUserUnmuteSuccess).toHaveBeenCalledExactlyOnceWith('alice');
    expect(wrapper.text()).not.toContain('ALICE');
    expect(wrapper.text()).toContain('BOB');
    expect(mocks.getMyMutedUsers).toHaveBeenCalledTimes(3);
    expect(useUserMuteStore(pinia).isMuted(8)).toBe(false);
  });

  it('shows an empty state when the account has no mutes', async () => {
    const wrapper = mountView(createPinia());
    await flushPromises();

    expect(wrapper.get('.muted-accounts-view__state').text()).toContain('You haven’t muted anyone.');
  });
});
