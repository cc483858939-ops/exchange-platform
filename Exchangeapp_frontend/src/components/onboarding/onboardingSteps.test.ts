// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';

const mocks = vi.hoisted(() => ({
  onboarding: null as any,
  authStore: null as any,
  profileStore: null as any,
  croppedFile: null as File | null,
  uploadAvatar: vi.fn(),
  updateProfile: vi.fn(),
  getTopics: vi.fn(),
  getSuggestedUsers: vi.fn(),
  followUser: vi.fn(),
  unfollowUser: vi.fn(),
  syncFollow: vi.fn(),
}));

vi.mock('../../store/onboarding', () => ({ useOnboardingStore: () => mocks.onboarding }));
vi.mock('../../store/auth', () => ({ useAuthStore: () => mocks.authStore }));
vi.mock('../../store/profileSession', () => ({ useProfileSessionStore: () => mocks.profileStore }));
vi.mock('../../services/userService', () => ({
  uploadProfileAvatar: mocks.uploadAvatar,
  updateUserProfile: mocks.updateProfile,
  getSuggestedUsers: mocks.getSuggestedUsers,
  followUser: mocks.followUser,
  unfollowUser: mocks.unfollowUser,
}));
vi.mock('../../services/topicService', () => ({ getTopics: mocks.getTopics }));
vi.mock('../../store/sessionSync', () => ({
  registerSuggestedUsersSessionSync: vi.fn(() => vi.fn()),
  syncExternalFollowState: mocks.syncFollow,
}));
vi.mock('../users/UserAvatar.vue', async () => {
  const { defineComponent, h } = await import('vue');
  return { default: defineComponent({
    name: 'UserAvatarStub',
    setup: () => () => h('span', { 'data-user-avatar': '' }),
  }) };
});
vi.mock('../profile/AvatarCropDialog.vue', async () => {
  const { defineComponent, h } = await import('vue');
  return { default: defineComponent({
    name: 'AvatarCropDialogStub',
    emits: ['apply', 'cancel'],
    setup(_props, { emit }) {
      return () => h('button', {
        type: 'button',
        'data-crop-apply': '',
        onClick: () => emit('apply', mocks.croppedFile),
      }, 'Apply crop');
    },
  }) };
});

import OnboardingAvatarStep from './OnboardingAvatarStep.vue';
import OnboardingDisplayNameStep from './OnboardingDisplayNameStep.vue';
import OnboardingInterestsStep from './OnboardingInterestsStep.vue';
import OnboardingFollowStep from './OnboardingFollowStep.vue';

const operation = { binding: { userID: 7, sessionID: 'session-7', sessionVersion: 1 }, generation: 1 };
const testUser = (id: number) => ({
  id,
  username: `person${id}`,
  display_name: `Person ${id}`,
  avatar_url: '',
  bio: 'A short introduction',
  created_at: '2026-01-01T00:00:00Z',
});

const mountStep = (component: any) => mount(component, { attachTo: document.body });

beforeEach(() => {
  const store = reactive({
    status: { completed: false, current_step: 'avatar', interest_slugs: [], completed_at: null },
    saving: false,
    error: '',
    beginOperation: vi.fn(() => { store.saving = true; return operation; }),
    captureContext: vi.fn(() => operation),
    matchesContext: vi.fn(() => true),
    finishOperation: vi.fn(() => { store.saving = false; }),
    saveProgress: vi.fn(async (step: string) => {
      store.status.current_step = step;
      store.error = '';
      return true;
    }),
    saveInterests: vi.fn(async (slugs: string[]) => ({
      completed: false,
      current_step: 'interests',
      interest_slugs: [...slugs].reverse(),
      completed_at: null,
    })),
    setError: vi.fn((message: string) => { store.error = message; }),
    clearError: vi.fn(() => { store.error = ''; }),
    complete: vi.fn(async () => true),
  });
  mocks.onboarding = store;
  mocks.authStore = {
    currentIdentity: reactive({ id: 7, username: 'viewer7', display_name: 'Viewer Seven', avatar_url: '/avatar.webp' }),
    syncCurrentIdentityProfile: vi.fn(() => true),
  };
  mocks.profileStore = { updateUser: vi.fn(() => true) };
  mocks.croppedFile = new File(['cropped'], 'avatar-cropped.png', { type: 'image/png' });
  mocks.uploadAvatar.mockReset();
  mocks.uploadAvatar.mockResolvedValue('/files/avatar.png');
  mocks.updateProfile.mockReset();
  mocks.updateProfile.mockImplementation(async (id, payload) => ({ ...testUser(id), ...payload, follower_count: 0, following_count: 0, cover_image_url: '' }));
  mocks.getTopics.mockReset();
  mocks.getTopics.mockResolvedValue({ items: [
    { slug: 'ai', label: 'AI', description: 'Artificial intelligence' },
    { slug: 'space', label: 'Space', description: 'Science and discovery' },
  ] });
  mocks.getSuggestedUsers.mockReset();
  mocks.getSuggestedUsers.mockResolvedValue({ items: [{ user: testUser(9), following: false }] });
  mocks.followUser.mockReset();
  mocks.followUser.mockResolvedValue({ user_id: 9, following: true, muted: false, follower_count: 1, following_count: 0 });
  mocks.unfollowUser.mockReset();
  mocks.unfollowUser.mockResolvedValue({ user_id: 9, following: false, muted: false, follower_count: 0, following_count: 0 });
  mocks.syncFollow.mockReset();
  vi.stubGlobal('URL', {
    createObjectURL: vi.fn(() => 'blob:avatar-preview'),
    revokeObjectURL: vi.fn(),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

describe('onboarding step components', () => {
  it('crops, uploads, saves the avatar profile, then advances; upload failures remain retryable', async () => {
    const wrapper = mountStep(OnboardingAvatarStep);
    const file = new File(['source'], 'source.jpg', { type: 'image/jpeg' });
    const input = wrapper.get('input[type="file"]');
    Object.defineProperty(input.element, 'files', { configurable: true, value: [file] });
    await input.trigger('change');
    await wrapper.get('[data-crop-apply]').trigger('click');
    mocks.uploadAvatar.mockRejectedValueOnce(new Error('offline'));

    await (wrapper.vm as any).next();
    expect(mocks.uploadAvatar).toHaveBeenCalledOnce();
    expect(mocks.updateProfile).not.toHaveBeenCalled();
    expect(mocks.onboarding.saveProgress).not.toHaveBeenCalled();
    expect(mocks.onboarding.error).toBe('Could not save your photo. Please retry.');

    await (wrapper.vm as any).next();
    expect(mocks.uploadAvatar).toHaveBeenCalledTimes(2);
    expect(mocks.updateProfile).toHaveBeenCalledWith(7, { avatar_url: '/files/avatar.png' });
    expect(mocks.authStore.syncCurrentIdentityProfile).toHaveBeenCalledOnce();
    expect(mocks.profileStore.updateUser).toHaveBeenCalledOnce();
    expect(mocks.onboarding.saveProgress).toHaveBeenCalledWith('display_name', operation);
    wrapper.unmount();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:avatar-preview');
  });

  it('rejects invalid avatar types and skips without uploading or patching the profile', async () => {
    const wrapper = mountStep(OnboardingAvatarStep);
    const input = wrapper.get('input[type="file"]');
    Object.defineProperty(input.element, 'files', { configurable: true, value: [new File(['x'], 'bad.gif', { type: 'image/gif' })] });
    await input.trigger('change');
    expect(mocks.onboarding.error).toBe('Use a JPEG, PNG, or WebP image.');

    await (wrapper.vm as any).skip();
    expect(mocks.uploadAvatar).not.toHaveBeenCalled();
    expect(mocks.updateProfile).not.toHaveBeenCalled();
    expect(mocks.onboarding.saveProgress).toHaveBeenCalledWith('display_name', operation);
    wrapper.unmount();
  });

  it('updates only display_name, leaves username read-only, blocks over-limit text and skips without PATCH', async () => {
    const wrapper = mountStep(OnboardingDisplayNameStep);
    await wrapper.get('#onboarding-display-name').setValue('  New display name  ');
    expect(wrapper.text()).toContain('@viewer7');
    await (wrapper.vm as any).next();
    expect(mocks.updateProfile).toHaveBeenCalledWith(7, { display_name: 'New display name' });
    expect(mocks.onboarding.saveProgress).toHaveBeenCalledWith('interests', operation);

    wrapper.unmount();
    mocks.updateProfile.mockClear();
    const overLimitWrapper = mountStep(OnboardingDisplayNameStep);
    await overLimitWrapper.get('#onboarding-display-name').setValue('😀'.repeat(51));
    expect((overLimitWrapper.vm as any).canAdvance).toBe(false);
    await (overLimitWrapper.vm as any).next();
    expect(mocks.updateProfile).not.toHaveBeenCalled();
    await (overLimitWrapper.vm as any).skip();
    expect(mocks.updateProfile).not.toHaveBeenCalled();
    overLimitWrapper.unmount();
  });

  it('loads dynamic topics, toggles multi-select and persists the selected slugs before progress', async () => {
    mocks.onboarding.status.interest_slugs = ['space'];
    const wrapper = mountStep(OnboardingInterestsStep);
    await flushPromises();
    expect(mocks.getTopics).toHaveBeenCalledOnce();
    const space = wrapper.findAll('button').find(button => button.text().includes('Space'))!;
    expect(space.attributes('aria-pressed')).toBe('true');
    const ai = wrapper.findAll('button').find(button => button.text().includes('AI'))!;
    await ai.trigger('click');
    expect(ai.attributes('aria-pressed')).toBe('true');
    await ai.trigger('click');
    expect(ai.attributes('aria-pressed')).toBe('false');
    await ai.trigger('click');
    await (wrapper.vm as any).next();

    expect(mocks.onboarding.saveInterests).toHaveBeenCalledWith(['space', 'ai'], operation);
    expect(mocks.onboarding.saveProgress).toHaveBeenCalledWith('follow_users', operation);
    wrapper.unmount();
  });

  it('allows interests to be skipped without replacing saved choices', async () => {
    mocks.onboarding.status.interest_slugs = ['space'];
    const wrapper = mountStep(OnboardingInterestsStep);
    await flushPromises();
    await wrapper.get('button').trigger('click');
    await (wrapper.vm as any).skip();
    expect(mocks.onboarding.saveInterests).not.toHaveBeenCalled();
    expect(mocks.onboarding.saveProgress).toHaveBeenCalledWith('follow_users', operation);
    wrapper.unmount();
  });

  it('follows a suggested user once and reflects the server state; empty suggestions still finish', async () => {
    const wrapper = mountStep(OnboardingFollowStep);
    await flushPromises();
    expect(mocks.getSuggestedUsers).toHaveBeenCalledWith(5);
    const follow = wrapper.findAll('button').find(button => button.text() === 'Follow')!;
    await follow.trigger('click');
    expect(mocks.followUser).toHaveBeenCalledOnce();
    expect(mocks.syncFollow).toHaveBeenCalledOnce();
    expect(wrapper.text()).toContain('Following');
    wrapper.unmount();

    mocks.getSuggestedUsers.mockResolvedValueOnce({ items: [] });
    const empty = mountStep(OnboardingFollowStep);
    await flushPromises();
    expect(empty.text()).toContain('No suggestions available right now.');
    await (empty.vm as any).next();
    expect(mocks.onboarding.complete).toHaveBeenCalledOnce();
    empty.unmount();
  });

  it('keeps the confirmed follow state after failure and allows the user to retry', async () => {
    mocks.followUser.mockRejectedValueOnce(new Error('offline'));
    const wrapper = mountStep(OnboardingFollowStep);
    await flushPromises();
    let follow = wrapper.findAll('button').find(button => button.text() === 'Follow')!;
    await follow.trigger('click');
    expect(wrapper.text()).toContain('Could not update follow status. Retry.');
    expect(follow.attributes('aria-pressed')).toBe('false');

    follow = wrapper.findAll('button').find(button => button.text() === 'Follow')!;
    await follow.trigger('click');
    expect(mocks.followUser).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain('Following');
    wrapper.unmount();
  });
});
