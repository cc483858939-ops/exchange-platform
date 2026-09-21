// @vitest-environment jsdom

import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('element-plus/es/components/message/style/css', () => ({}));
import router, { resolveAuthenticatedGuestOnlyDestination } from './index';
import { routeScrollBehavior } from './scrollBehavior';

const setAuthenticatedState = (id = 42) => {
  localStorage.setItem('token', 'access-token');
  localStorage.setItem('refresh_token', 'refresh-token');
  localStorage.setItem('auth_user', JSON.stringify({
    id,
    username: 'alice',
    display_name: 'Alice',
    avatar_url: '',
  }));
  setActivePinia(createPinia());
};

beforeEach(() => {
  localStorage.clear();
  setActivePinia(createPinia());
});

afterEach(() => {
  localStorage.clear();
});

describe('History route', () => {
  it('configures the centralized scroll policy', () => {
    expect(router.options.scrollBehavior).toBe(routeScrollBehavior);
  });

  it('registers the private history surface in the app layout', () => {
    const route = router.getRoutes().find(item => item.name === 'History');
    expect(route?.path).toBe('/history');
    expect(route?.meta.layout).toBe('app');
  });

  it('preserves every route contract while loading views lazily', async () => {
    const expectedRoutes = [
      ['Home', '/', 'app', 'Home'],
      ['CurrencyExchange', '/exchange', 'app', 'Currency Exchange'],
      ['PostCreate', '/posts/new', 'app', 'Post'],
      ['PostDetail', '/posts/:id', 'app', 'Post'],
      ['UserProfile', '/users/:id', 'app', 'Profile'],
      ['UserFollowing', '/users/:id/following', 'app', 'Following'],
      ['UserFollowers', '/users/:id/followers', 'app', 'Followers'],
      ['UserSearch', '/search', 'app', 'Search'],
      ['History', '/history', 'app', 'History'],
      ['Notifications', '/notifications', 'app', 'Notifications'],
      ['Login', '/login', 'auth', 'Log in'],
      ['Register', '/register', 'auth', 'Sign up'],
      ['NotFound', '/:pathMatch(.*)*', 'app', 'Page not found'],
    ] as const;

    for (const [name, path, layout, title] of expectedRoutes) {
      const route = router.getRoutes().find(item => item.name === name);
      expect(route?.path).toBe(path);
      expect(route?.meta.layout).toBe(layout);
      expect(route?.meta.title).toBe(title);
      expect(typeof route?.components?.default).toBe('function');
    }

    expect(router.getRoutes().find(item => item.name === 'Login')?.meta.guestOnly).toBe(true);
    expect(router.getRoutes().find(item => item.name === 'Register')?.meta.guestOnly).toBe(true);

    const home = router.getRoutes().find(item => item.name === 'Home');
    const loaded = (home?.components?.default as () => Promise<unknown>)?.();
    expect(loaded).toBeInstanceOf(Promise);
    await expect(loaded).resolves.toBeTruthy();
  });

  it('updates the browser title after successful navigation', async () => {
    await router.push('/notifications');
    await router.isReady();
    expect(document.title).toBe('Notifications — Exchange');

    await router.push('/posts/42');
    expect(document.title).toBe('Post — Exchange');

    await router.push('/missing-route');
    expect(document.title).toBe('Page not found — Exchange');
  });

  it('resolves an unknown URL to NotFound without redirecting', () => {
    const resolved = router.resolve('/definitely-not-a-route');

    expect(resolved.name).toBe('NotFound');
    expect(resolved.fullPath).toBe('/definitely-not-a-route');
  });

  it('leaves the retired Bookmarks URL to the catch-all NotFound route', () => {
    const resolved = router.resolve('/bookmarks');

    expect(resolved.name).toBe('NotFound');
    expect(resolved.fullPath).toBe('/bookmarks');
    expect(router.getRoutes().some(route => route.name === 'Bookmarks')).toBe(false);
  });

  it('preserves query and hash when resolving an unknown URL', () => {
    const resolved = router.resolve('/missing-page?from=test#section');

    expect(resolved.name).toBe('NotFound');
    expect(resolved.fullPath).toBe('/missing-page?from=test#section');
  });

  it('keeps known static, dynamic, and create routes ahead of the catch-all', () => {
    expect(router.resolve('/notifications').name).toBe('Notifications');
    expect(router.resolve('/posts/42').name).toBe('PostDetail');
    expect(router.resolve('/posts/new').name).toBe('PostCreate');
  });
});

describe('Login return target navigation', () => {
  beforeEach(async () => {
    if (router.currentRoute.value.fullPath !== '/') {
      await router.push('/');
    }
  });

  afterEach(async () => {
    if (router.currentRoute.value.fullPath !== '/') {
      await router.push('/');
    }
  });

  it('captures an app route fullPath when entering Login', async () => {
    await router.push('/notifications?filter=unread#top');
    await router.push({ name: 'Login' });

    expect(router.currentRoute.value.query.returnTo).toBe('/notifications?filter=unread#top');
  });

  it('preserves Post query and hash in returnTo', async () => {
    await router.push('/posts/42?reply=1#conversation');
    await router.push({ name: 'Login' });

    expect(router.currentRoute.value.query.returnTo).toBe('/posts/42?reply=1#conversation');
  });

  it('does not capture an auth-layout source route', async () => {
    await router.push('/register');
    await router.push({ name: 'Login' });

    expect(router.currentRoute.value.query.returnTo).toBeUndefined();
  });

  it('does not overwrite an explicit returnTo query', async () => {
    await router.push('/notifications');
    await router.push({
      name: 'Login',
      query: { returnTo: '/search?q=alice' },
    });

    expect(router.currentRoute.value.query.returnTo).toBe('/search?q=alice');
  });
});

describe('guest-only authentication routes', () => {
  beforeEach(async () => {
    if (router.currentRoute.value.fullPath !== '/') {
      await router.push('/');
    }
  });

  it('allows a guest to open Login', async () => {
    await router.push('/login');

    expect(router.currentRoute.value.name).toBe('Login');
  });

  it('allows a guest to open Register', async () => {
    await router.push('/register');

    expect(router.currentRoute.value.name).toBe('Register');
  });

  it('redirects an authenticated user from Login to Home by default', async () => {
    setAuthenticatedState();

    await router.push('/login');

    expect(router.currentRoute.value.name).toBe('Home');
  });

  it('redirects an authenticated user from Register to Home without entering Register', async () => {
    setAuthenticatedState(7);

    await router.push('/register');

    expect(router.currentRoute.value.name).toBe('Home');
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null').id).toBe(7);
  });

  it('preserves a safe Login returnTo target for authenticated users', async () => {
    setAuthenticatedState();

    await router.push({ name: 'Login', query: { returnTo: '/notifications' } });

    expect(router.currentRoute.value.fullPath).toBe('/notifications');
  });

  it('preserves a safe PostDetail returnTo target for authenticated users', async () => {
    setAuthenticatedState();

    await router.push({ name: 'Login', query: { returnTo: '/posts/123' } });

    expect(router.currentRoute.value.fullPath).toBe('/posts/123');
  });

  it('rejects an unsafe Login returnTo target for authenticated users', async () => {
    setAuthenticatedState();

    await router.push({ name: 'Login', query: { returnTo: 'https://evil.example' } });

    expect(router.currentRoute.value.name).toBe('Home');
  });

  it('routes authenticated profile intent to the current user profile', async () => {
    setAuthenticatedState(42);

    await router.push({ name: 'Login', query: { intent: 'profile' } });

    expect(router.currentRoute.value.fullPath).toBe('/users/42');
  });

  it('falls back to Home when profile intent has no usable identity ID', () => {
    const destination = resolveAuthenticatedGuestOnlyDestination(
      router,
      router.resolve({ name: 'Login', query: { intent: 'profile' } }),
      { id: 0 },
    );

    expect(destination).toEqual({ name: 'Home' });
  });

  it('settles an authenticated Home-to-Login navigation without a redirect loop', async () => {
    setAuthenticatedState();

    await router.push('/login');
    expect(router.currentRoute.value.fullPath).toBe('/');
  });
});
