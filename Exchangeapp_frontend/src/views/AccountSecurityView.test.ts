// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import AccountSecurityView from './AccountSecurityView.vue';

type MockBinding = { userID: number; sessionID: string; sessionVersion: number };
type MockAuthStore = {
  isAuthenticated: boolean;
  currentIdentity: { id: number; username: string } | null;
  sessionID: string;
  sessionVersion: number;
  token: string;
  captureRequestAuthBinding: ReturnType<typeof vi.fn>;
  matchesRequestAuthBinding: ReturnType<typeof vi.fn>;
  changePassword: ReturnType<typeof vi.fn>;
};

const mocks = vi.hoisted(() => ({
  authStore: {} as MockAuthStore,
  router: { back: vi.fn(), push: vi.fn(), replace: vi.fn() },
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', async (importOriginal) => {
  const original = await importOriginal<typeof import('../store/auth')>();
  return { ...original, useAuthStore: () => mocks.authStore };
});

const mountPage = () => mount(AccountSecurityView, {
  global: {
    stubs: {
      AppIcon: { props: ['name'], template: '<span :data-icon="name" />' },
      MobileAccountMenu: true,
      RouterLink: { props: ['to'], template: '<a :data-route-name="to && to.name"><slot /></a>' },
    },
  },
});

describe('AccountSecurityView', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  const setSession = (userID: number, username: string) => {
    mocks.authStore.sessionID = `sid-${userID}-${mocks.authStore.sessionVersion + 1}`;
    mocks.authStore.sessionVersion += 1;
    mocks.authStore.currentIdentity = { id: userID, username };
    mocks.authStore.isAuthenticated = true;
  };

  const clearSession = () => {
    mocks.authStore.sessionVersion += 1;
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.currentIdentity = null;
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore = reactive({
      isAuthenticated: true,
      currentIdentity: { id: 7, username: 'alice' },
      sessionID: 'sid-7-0',
      sessionVersion: 0,
      token: 'Bearer alice-access',
      captureRequestAuthBinding: vi.fn(() => {
        if (!mocks.authStore.isAuthenticated || !mocks.authStore.currentIdentity) return null;
        return Object.freeze({
          userID: mocks.authStore.currentIdentity.id,
          sessionID: mocks.authStore.sessionID,
          sessionVersion: mocks.authStore.sessionVersion,
        });
      }),
      matchesRequestAuthBinding: vi.fn((candidate: MockBinding | null) => {
        if (!candidate || !mocks.authStore.isAuthenticated || !mocks.authStore.currentIdentity) return false;
        return candidate.userID === mocks.authStore.currentIdentity.id
          && candidate.sessionID === mocks.authStore.sessionID
          && candidate.sessionVersion === mocks.authStore.sessionVersion;
      }),
      changePassword: vi.fn(),
    }) as MockAuthStore;
    mocks.authStore.changePassword.mockResolvedValue(undefined);
    mocks.router.replace.mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  const setPasswords = async (current: string, next: string, confirmation = next) => {
    await wrapper!.get('input[autocomplete="current-password"]').setValue(current);
    await wrapper!.get('input[autocomplete="new-password"]').setValue(next);
    await wrapper!.findAll('input[autocomplete="new-password"]')[1].setValue(confirmation);
  };

  it('uses UTF-8 bytes for ASCII, CJK and emoji password limits', async () => {
    wrapper = mountPage();
    await setPasswords('old', 'a'.repeat(72));
    expect(wrapper.get('#password-byte-count').text()).toBe('72 / 72 UTF-8 bytes');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined();

    await wrapper.get('input[autocomplete="new-password"]').setValue('a'.repeat(73));
    await wrapper.findAll('input[autocomplete="new-password"]')[1].setValue('a'.repeat(73));
    expect(wrapper.get('#password-byte-count').text()).toBe('73 / 72 UTF-8 bytes');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();

    await wrapper.get('input[autocomplete="new-password"]').setValue('汉'.repeat(24));
    await wrapper.findAll('input[autocomplete="new-password"]')[1].setValue('汉'.repeat(24));
    expect(wrapper.get('#password-byte-count').text()).toBe('72 / 72 UTF-8 bytes');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined();

    await wrapper.get('input[autocomplete="new-password"]').setValue('😀'.repeat(18));
    await wrapper.findAll('input[autocomplete="new-password"]')[1].setValue('😀'.repeat(18));
    expect(wrapper.get('#password-byte-count').text()).toBe('72 / 72 UTF-8 bytes');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined();
  });

  it('requires matching confirmation and a different new password before sending', async () => {
    wrapper = mountPage();
    await setPasswords('same', 'different', 'not-matching');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();
    await wrapper.get('form').trigger('submit');
    expect(wrapper.get('[role="alert"]').text()).toBe('New password confirmation does not match.');
    expect(mocks.authStore.changePassword).not.toHaveBeenCalled();

    await setPasswords('same', 'same');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();
    await wrapper.get('form').trigger('submit');
    expect(wrapper.get('[role="alert"]').text()).toBe('Choose a password different from your current password.');
    expect(mocks.authStore.changePassword).not.toHaveBeenCalled();
  });

  it('submits once with the captured identity binding, then navigates after the store clears the session', async () => {
    let resolveChange!: () => void;
    mocks.authStore.changePassword.mockReturnValueOnce(new Promise<void>(resolve => {
      resolveChange = () => { clearSession(); resolve(); };
    }));
    wrapper = mountPage();
    await setPasswords('current', 'new-password');
    await wrapper.get('form').trigger('submit');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();
    await wrapper.get('form').trigger('submit');
    expect(mocks.authStore.changePassword).toHaveBeenCalledTimes(1);
    resolveChange();
    await flushPromises();
    expect(mocks.authStore.changePassword).toHaveBeenCalledWith('current', 'new-password', {
      userID: 7,
      sessionID: 'sid-7-0',
      sessionVersion: 0,
    });
    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'Login', query: { passwordChanged: '1' } });
  });

  it('shows server errors and does not send a password longer than 72 UTF-8 bytes', async () => {
    wrapper = mountPage();
    await setPasswords('current', '😀'.repeat(19));
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();
    await wrapper.get('form').trigger('submit');
    expect(mocks.authStore.changePassword).not.toHaveBeenCalled();

    await setPasswords('current', 'new-password');
    mocks.authStore.changePassword.mockRejectedValueOnce(new Error('Current password is incorrect.'));
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toBe('Current password is incorrect.');
  });

  it('clears the old account form and refuses to submit it after an account switch', async () => {
    wrapper = mountPage();
    await setPasswords('alice-current', 'alice-new');

    setSession(8, 'bob');
    await wrapper.vm.$nextTick();

    expect(wrapper.get('input[autocomplete="current-password"]').element).toHaveProperty('value', '');
    expect(wrapper.get('input[autocomplete="new-password"]').element).toHaveProperty('value', '');
    expect(wrapper.findAll('input[autocomplete="new-password"]')[1].element).toHaveProperty('value', '');
    await wrapper.get('form').trigger('submit');
    expect(mocks.authStore.changePassword).not.toHaveBeenCalled();
  });

  it('does not reset the form when only the access token rotates', async () => {
    wrapper = mountPage();
    await setPasswords('current', 'new-password');

    mocks.authStore.token = 'Bearer alice-refreshed-access';
    await wrapper.vm.$nextTick();

    expect(wrapper.get('input[autocomplete="current-password"]').element).toHaveProperty('value', 'current');
    expect(wrapper.get('input[autocomplete="new-password"]').element).toHaveProperty('value', 'new-password');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined();
  });

  it('does not let an old successful request navigate or finish a new account submission', async () => {
    let resolveAlice!: () => void;
    let resolveBob!: () => void;
    mocks.authStore.changePassword
      .mockReturnValueOnce(new Promise<void>(resolve => { resolveAlice = resolve; }))
      .mockReturnValueOnce(new Promise<void>(resolve => { resolveBob = resolve; }));
    wrapper = mountPage();

    await setPasswords('alice-current', 'alice-new');
    const aliceSubmission = wrapper.get('form').trigger('submit');
    expect(mocks.authStore.changePassword).toHaveBeenCalledTimes(1);

    setSession(8, 'bob');
    await wrapper.vm.$nextTick();
    await setPasswords('bob-current', 'bob-new');
    const bobSubmission = wrapper.get('form').trigger('submit');
    expect(mocks.authStore.changePassword).toHaveBeenCalledTimes(2);

    resolveAlice();
    await aliceSubmission;
    await flushPromises();
    expect(wrapper.get('input[autocomplete="current-password"]').element).toHaveProperty('value', 'bob-current');
    expect(wrapper.get('input[autocomplete="new-password"]').element).toHaveProperty('value', 'bob-new');
    expect(wrapper.get('button[type="submit"]').attributes('aria-busy')).toBe('true');
    expect(mocks.router.replace).not.toHaveBeenCalled();

    resolveBob();
    await bobSubmission;
    await flushPromises();
  });

  it('ignores an old request failure after a different account becomes active', async () => {
    let rejectAlice!: (error: Error) => void;
    mocks.authStore.changePassword.mockReturnValueOnce(new Promise<void>((_resolve, reject) => { rejectAlice = reject; }));
    wrapper = mountPage();

    await setPasswords('alice-current', 'alice-new');
    const submission = wrapper.get('form').trigger('submit');
    setSession(8, 'bob');
    await wrapper.vm.$nextTick();
    rejectAlice(new Error('Alice password error'));
    await submission;
    await flushPromises();

    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.get('input[autocomplete="current-password"]').element).toHaveProperty('value', '');
    expect(mocks.router.replace).not.toHaveBeenCalled();
  });

  it('resets the form and prevents requests when the active session logs out', async () => {
    wrapper = mountPage();
    await setPasswords('current', 'new-password');

    clearSession();
    await wrapper.vm.$nextTick();

    expect(wrapper.find('form').exists()).toBe(false);
    expect(mocks.authStore.changePassword).not.toHaveBeenCalled();
  });
});
