// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import AccountSecurityView from './AccountSecurityView.vue';

const mocks = vi.hoisted(() => ({
  authStore: {
    isAuthenticated: true,
    currentIdentity: { id: 7, username: 'alice' },
    changePassword: vi.fn(),
  },
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

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore.changePassword.mockReset();
    mocks.authStore.isAuthenticated = true;
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

  it('requires matching confirmation and a different new password', async () => {
    wrapper = mountPage();
    await setPasswords('same', 'different', 'not-matching');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();
    await setPasswords('same', 'same');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();
  });

  it('submits once, clears the session through the store, and navigates to Login', async () => {
    let resolveChange!: () => void;
    mocks.authStore.changePassword.mockReturnValueOnce(new Promise<void>(resolve => { resolveChange = resolve; }));
    wrapper = mountPage();
    await setPasswords('current', 'new-password');
    await wrapper.get('form').trigger('submit');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined();
    await wrapper.get('form').trigger('submit');
    expect(mocks.authStore.changePassword).toHaveBeenCalledTimes(1);
    resolveChange();
    await flushPromises();
    expect(mocks.authStore.changePassword).toHaveBeenCalledWith('current', 'new-password');
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
});
