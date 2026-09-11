// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthRequestError } from '../utils/authError';
import Register from './Register.vue';

const mocks = vi.hoisted(() => ({
  authStore: {
    register: vi.fn(),
  },
  router: {
    push: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

const mountRegister = () => mount(Register, {
  attachTo: document.body,
  global: {
    stubs: {
      RouterLink: {
        props: ['to'],
        template: '<a><slot /></a>',
      },
    },
  },
});

describe('Register errors', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.authStore.register.mockResolvedValue(undefined);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    document.body.innerHTML = '';
  });

  const submit = async () => {
    await wrapper!.get('#register-username').setValue('alice');
    await wrapper!.get('#register-password').setValue('secret123');
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
  };

  it('shows the username-specific error and preserves the form after a conflict', async () => {
    mocks.authStore.register.mockRejectedValueOnce(
      new AuthRequestError('Username is unavailable', 'AUTH_USERNAME_UNAVAILABLE'),
    );
    wrapper = mountRegister();

    await submit();

    expect(wrapper.get('.auth-error').text()).toBe('That username is already in use. Choose another username.');
    expect(wrapper.text()).not.toContain('Could not create account. Please try again.');
    expect(mocks.router.push).not.toHaveBeenCalled();
    expect(mocks.authStore.register).toHaveBeenCalledWith('alice', 'secret123');
    expect((wrapper.get('#register-username').element as HTMLInputElement).value).toBe('alice');
    expect((wrapper.get('#register-password').element as HTMLInputElement).value).toBe('secret123');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined();
    expect(wrapper.get('button[type="submit"]').text()).toBe('Sign up');
  });

  it('keeps unrelated registration failures generic', async () => {
    mocks.authStore.register.mockRejectedValueOnce(
      new AuthRequestError('Authentication failed', 'AUTH_INTERNAL'),
    );
    wrapper = mountRegister();

    await submit();

    expect(wrapper.get('.auth-error').text()).toBe('Could not create account. Please try again.');
    expect(wrapper.text()).not.toContain('That username is already in use. Choose another username.');
  });

  it('maps invalid request errors to the existing validation guidance', async () => {
    mocks.authStore.register.mockRejectedValueOnce(
      new AuthRequestError('Invalid request data', 'AUTH_REQUEST_INVALID'),
    );
    wrapper = mountRegister();

    await submit();

    expect(wrapper.get('.auth-error').text()).toBe('Enter a username and password.');
  });
});
