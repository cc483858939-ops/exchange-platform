import { beforeEach, describe, expect, it } from 'vitest';
import {
  normalizeSearchRouteSnapshot,
  rememberSearchRoute,
  rememberedSearchRouteSnapshot,
  searchNavigationDestination,
} from './searchRouteState';

describe('Search route state', () => {
  beforeEach(() => rememberSearchRoute({}));

  it('keeps canonical Post Search fields and excludes cursor-like state', () => {
    const from = '2026-09-01T00:00:00.000Z';
    const to = '2026-09-02T00:00:00.000Z';
    expect(normalizeSearchRouteSnapshot({
      tab: 'posts', q: ' 日元 ', author: '042', time: 'custom', from, to, cursor: 'ignored',
    })).toEqual({ tab: 'posts', q: '日元', author: '42', time: 'custom', from, to });
  });

  it('restores the remembered Post route', () => {
    rememberSearchRoute({ tab: 'posts', q: '日元', author: '42', time: '7d' });
    expect(searchNavigationDestination(true, { name: 'PostDetail' })).toEqual({
      name: 'UserSearch',
      query: { tab: 'posts', q: '日元', author: '42', time: '7d' },
    });
  });

  it('restores the remembered People route', () => {
    rememberSearchRoute({ tab: 'people', q: 'alice' });
    expect(searchNavigationDestination(true, { name: 'PostDetail' })).toEqual({
      name: 'UserSearch',
      query: { tab: 'people', q: 'alice' },
    });
  });

  it('uses bare Search as the final fallback', () => {
    expect(searchNavigationDestination(true, { name: 'PostDetail' })).toEqual({ name: 'UserSearch' });
  });

  it('keeps a bare Search visit after previously searching for People', () => {
    rememberSearchRoute({ tab: 'people', q: 'alice' });
    rememberSearchRoute({});
    expect(searchNavigationDestination(true, { name: 'Home' })).toEqual({ name: 'UserSearch' });
    expect(searchNavigationDestination(false, { name: 'Home' })).toEqual({
      name: 'Login', query: { returnTo: '/search' },
    });
  });

  it('preserves custom dates in a guest returnTo and keeps an active guest route verbatim', () => {
    const from = '2026-09-01T00:00:00.000Z';
    const to = '2026-09-02T00:00:00.000Z';
    rememberSearchRoute({ tab: 'posts', q: '日元', author: '42', time: 'custom', from, to });
    expect(searchNavigationDestination(false, { name: 'PostDetail' })).toEqual({
      name: 'Login',
      query: {
        returnTo: '/search?tab=posts&q=%E6%97%A5%E5%85%83&author=42&time=custom&from=2026-09-01T00%3A00%3A00.000Z&to=2026-09-02T00%3A00%3A00.000Z',
      },
    });
    expect(searchNavigationDestination(false, {
      name: 'UserSearch',
      fullPath: '/search?tab=people&q=alice',
      query: { tab: 'posts', q: 'ignored' },
    })).toEqual({
      name: 'Login',
      query: { returnTo: '/search?tab=people&q=alice' },
    });
    expect(rememberedSearchRouteSnapshot.value?.tab).toBe('posts');
  });
});
