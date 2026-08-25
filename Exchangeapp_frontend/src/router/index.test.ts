// @vitest-environment jsdom

import { describe, expect, it } from 'vitest';
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
      ['ArticleCreate', '/news/new', 'app'],
      ['NewsDetail', '/news/:id', 'app'],
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
