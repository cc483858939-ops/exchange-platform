// @vitest-environment jsdom

import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Login from './Login.vue';

const mocks = vi.hoisted(() => ({
  route: { query: {} as Record<string, unknown> },
  router: {
    replace: vi.fn(),
    push: vi.fn(),
    resolve: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    login: vi.fn(),
  },
}));

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => mocks.router,
}));

vi.mock('../store/auth', () => ({
  useAuthStore: () => mocks.authStore,
}));

const validTargets = new Set([
  '/posts/42?reply=1#conversation',
  '/notifications',
]);

const mountLogin = () => mount(Login, {
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

describe('Login return navigation', () => {
  let wrapper: ReturnType<typeof mount> | null = null;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.route.query = {};
    mocks.authStore.isAuthenticated = false;
    mocks.authStore.login.mockResolvedValue(undefined);
    mocks.router.resolve.mockImplementation((candidate: string) => ({
      fullPath: candidate,
      matched: validTargets.has(candidate) ? [{}] : [],
      meta: { layout: 'app' },
    }));
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    document.body.innerHTML = '';
  });

  const submit = async () => {
    await wrapper!.get('#login-username').setValue('alice');
    await wrapper!.get('#login-password').setValue('secret123');
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
  };

  it('replaces to a valid return target after successful login', async () => {
    mocks.route.query = { returnTo: '/posts/42?reply=1#conversation' };
    wrapper = mountLogin();

    await submit();

    expect(mocks.authStore.login).toHaveBeenCalledWith('alice', 'secret123');
    expect(mocks.router.replace).toHaveBeenCalledWith('/posts/42?reply=1#conversation');
    expect(mocks.router.push).not.toHaveBeenCalled();
  });

  it('replaces to Home when no return target exists', async () => {
    wrapper = mountLogin();

    await submit();

    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'Home' });
  });

  it('stays on Login and keeps the target when authentication fails', async () => {
    mocks.route.query = { returnTo: '/notifications' };
    mocks.authStore.login.mockRejectedValueOnce(new Error('Invalid username or password'));
    wrapper = mountLogin();

    await submit();

    expect(mocks.router.replace).not.toHaveBeenCalled();
    expect(wrapper.find('.auth-error').text()).toBe('Invalid username or password.');
    expect(mocks.route.query.returnTo).toBe('/notifications');
  });

  it('falls back to Home for an invalid return target', async () => {
    mocks.route.query = { returnTo: 'https://evil.example' };
    wrapper = mountLogin();

    await submit();

    expect(mocks.router.replace).toHaveBeenCalledWith({ name: 'Home' });
  });

  it('redirects an already-authenticated user on initial mount', () => {
    mocks.authStore.isAuthenticated = true;
    mocks.route.query = { returnTo: '/notifications' };

    wrapper = mountLogin();

    expect(mocks.router.replace).toHaveBeenCalledWith('/notifications');
    expect(mocks.authStore.login).not.toHaveBeenCalled();
  });
});
