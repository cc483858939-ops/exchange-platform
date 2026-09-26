// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthRequestError } from '../utils/authError';
import Register from './Register.vue';

const mocks = vi.hoisted(() => ({
  route: { query: {} as Record<string, unknown> },
  authStore: {
    currentIdentity: null as { id: number } | null,
    register: vi.fn(),
  },
  router: {
    push: vi.fn(),
    replace: vi.fn(),
    resolve: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
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
        template: '<a :data-to="JSON.stringify(to)"><slot /></a>',
      },
    },
  },
});

describe('Register navigation and errors', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route.query = {};
    mocks.authStore.currentIdentity = null;
    mocks.authStore.register.mockResolvedValue(undefined);
    mocks.router.resolve.mockImplementation((candidate: string) => ({
      fullPath: candidate,
      matched: candidate === '/notifications' ? [{}] : [],
      meta: { layout: 'app' },
    }));
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

  it('carries only supported auth context into Login', () => {
    mocks.route.query = {
      returnTo: '/notifications',
      intent: 'profile',
      source: 'feed',
      campaign: 'spring',
    };
    wrapper = mountRegister();

    expect(JSON.parse(wrapper.get('.auth-switch a').attributes('data-to')!)).toEqual({
      name: 'Login',
      query: {
        returnTo: '/notifications',
        intent: 'profile',
      },
    });
  });

  it('replaces to the preserved returnTo destination after successful registration', async () => {
    mocks.route.query = { returnTo: '/notifications' };
    wrapper = mountRegister();

    await submit();

    expect(mocks.router.replace).toHaveBeenCalledWith('/notifications');
    expect(mocks.router.push).not.toHaveBeenCalled();
  });

  it('uses the shared profile destination after registration', async () => {
    mocks.route.query = { intent: 'profile' };
    mocks.authStore.currentIdentity = { id: 42 };
    wrapper = mountRegister();

    await submit();

    expect(mocks.router.replace).toHaveBeenCalledWith({
      name: 'UserProfile',
      params: { id: '42' },
    });
  });

  it('replaces to Home after successful registration without supported context', async () => {
    wrapper = mountRegister();

    await submit();

    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'Home' });
    expect(mocks.router.push).not.toHaveBeenCalled();
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

  it('releases the submit button and shows retry guidance after a timeout', async () => {
    mocks.authStore.register.mockRejectedValueOnce(
      new AuthRequestError(
        'Request timed out. Check your connection and try again.',
        'AUTH_REQUEST_TIMEOUT',
      ),
    );
    wrapper = mountRegister();

    await submit();

    expect(wrapper.get('.auth-error').text()).toBe('Request timed out. Check your connection and try again.');
    expect(wrapper.get('button[type="submit"]').text()).toBe('Sign up');
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined();
    expect(mocks.router.push).not.toHaveBeenCalled();
  });
});
