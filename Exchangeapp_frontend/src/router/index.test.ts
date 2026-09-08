// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import router from './index';
import { routeScrollBehavior } from './scrollBehavior';

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
    await router.push('/');
  });

  afterEach(async () => {
    await router.push('/');
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
