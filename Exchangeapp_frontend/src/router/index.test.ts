// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import router from './index';

describe('History route', () => {
  it('registers the private history surface in the app layout', () => {
    const route = router.getRoutes().find(item => item.name === 'History');
    expect(route?.path).toBe('/history');
    expect(route?.meta.layout).toBe('app');
  });

  it('preserves every route contract while loading views lazily', async () => {
    const expectedRoutes = [
      ['Home', '/', 'app'],
      ['CurrencyExchange', '/exchange', 'app'],
      ['PostCreate', '/posts/new', 'app'],
      ['PostDetail', '/posts/:id', 'app'],
      ['UserProfile', '/users/:id', 'app'],
      ['UserFollowing', '/users/:id/following', 'app'],
      ['UserFollowers', '/users/:id/followers', 'app'],
      ['UserSearch', '/search', 'app'],
      ['History', '/history', 'app'],
      ['Notifications', '/notifications', 'app'],
      ['Login', '/login', 'auth'],
      ['Register', '/register', 'auth'],
    ] as const;

    for (const [name, path, layout] of expectedRoutes) {
      const route = router.getRoutes().find(item => item.name === name);
      expect(route?.path).toBe(path);
      expect(route?.meta.layout).toBe(layout);
      expect(typeof route?.components?.default).toBe('function');
    }

    const home = router.getRoutes().find(item => item.name === 'Home');
    const loaded = (home?.components?.default as () => Promise<unknown>)?.();
    expect(loaded).toBeInstanceOf(Promise);
    await expect(loaded).resolves.toBeTruthy();
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
