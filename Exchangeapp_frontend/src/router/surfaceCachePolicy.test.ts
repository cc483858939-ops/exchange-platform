import { describe, expect, it } from 'vitest';
import type { RouteLocationNormalizedLoaded } from 'vue-router';
import {
  getExternalProfileCacheKey,
  getRootSurfaceCacheKey,
  getViewerCacheNamespace,
  isViewOwnedScrollRoute,
  shouldPreserveExternalProfileCache,
} from './surfaceCachePolicy';

const location = (
  name: string,
  params: Record<string, string | string[]> = {},
  query: Record<string, string | string[]> = {},
) => ({ name, params, query } as unknown as RouteLocationNormalizedLoaded);

describe('surface cache policy', () => {
  it.each([
    ['Home', 'root:home'],
    ['UserSearch', 'root:search'],
    ['CurrencyExchange', 'root:exchange'],
    ['Notifications', 'root:notifications'],
  ])('maps %s to %s', (name, key) => {
    expect(getRootSurfaceCacheKey(location(name), 7)).toBe(key);
  });

  it('keeps search query changes in the same root cache entry', () => {
    expect(getRootSurfaceCacheKey(location('UserSearch', {}, { q: 'alice' }), 7))
      .toBe('root:search');
    expect(getRootSurfaceCacheKey(location('UserSearch', {}, { q: 'bob' }), 7))
      .toBe('root:search');
  });

  it('separates the own profile root from external profiles', () => {
    expect(getRootSurfaceCacheKey(location('UserProfile', { id: '7' }), 7))
      .toBe('root:profile:7');
    expect(getRootSurfaceCacheKey(location('UserProfile', { id: ['7'] }), 7))
      .toBe('root:profile:7');
    expect(getRootSurfaceCacheKey(location('UserProfile', { id: '8' }), 7))
      .toBeNull();
    expect(getExternalProfileCacheKey(location('UserProfile', { id: '8' }), 7))
      .toBe('external-profile:8');
  });

  it('allows anonymous external profile caching and rejects invalid IDs', () => {
    expect(getExternalProfileCacheKey(location('UserProfile', { id: '8' }), null))
      .toBe('external-profile:8');
    expect(getExternalProfileCacheKey(location('UserProfile', { id: '0' }), 7)).toBeNull();
    expect(getExternalProfileCacheKey(location('UserProfile', { id: 'not-a-number' }), 7)).toBeNull();
    expect(getExternalProfileCacheKey(location('UserProfile', { id: ['8', '9'] }), 7))
      .toBe('external-profile:8');
  });

  it('returns no cache component key for transient routes', () => {
    expect(getRootSurfaceCacheKey(location('PostDetail', { id: '42' }), 7)).toBeNull();
    expect(getExternalProfileCacheKey(location('PostDetail', { id: '42' }), 7)).toBeNull();
    expect(getRootSurfaceCacheKey(location('History'), 7)).toBeNull();
  });

  it('preserves external profile cache only through the profile return flow', () => {
    expect(shouldPreserveExternalProfileCache(location('UserProfile', { id: '8' }))).toBe(true);
    expect(shouldPreserveExternalProfileCache(location('PostDetail', { id: '42' }))).toBe(true);
    expect(shouldPreserveExternalProfileCache(location('UserFollowing', { id: '8' }))).toBe(true);
    expect(shouldPreserveExternalProfileCache(location('UserFollowers', { id: '8' }))).toBe(true);
    expect(shouldPreserveExternalProfileCache(location('Home'))).toBe(false);
    expect(shouldPreserveExternalProfileCache(location('Search'))).toBe(false);
  });

  it('uses stable viewer namespaces', () => {
    expect(getViewerCacheNamespace(7)).toBe('viewer:7');
    expect(getViewerCacheNamespace(null)).toBe('anonymous');
  });

  it.each([
    'Home',
    'UserSearch',
    'CurrencyExchange',
    'Notifications',
    'UserProfile',
  ])('lets %s own scroll restoration', (name) => {
    expect(isViewOwnedScrollRoute(location(name))).toBe(true);
  });

  it.each(['History', 'PostDetail', 'PostCreate', 'UserFollowing', 'UserFollowers'])
    ('leaves %s to router scroll restoration', (name) => {
      expect(isViewOwnedScrollRoute(location(name))).toBe(false);
    });
});
