import { describe, expect, it } from 'vitest';
import type { RouteLocationNormalized, RouteLocationNormalizedLoaded, RouterScrollBehavior } from 'vue-router';
import { routeScrollBehavior } from './scrollBehavior';

const location = (
  name: string,
  params: Record<string, string> = {},
  query: Record<string, string> = {},
) => ({
  name,
  params,
  query,
}) as unknown as RouteLocationNormalized & RouteLocationNormalizedLoaded;

const applyScrollBehavior = (
  to: ReturnType<typeof location>,
  from: ReturnType<typeof location>,
  savedPosition: Parameters<RouterScrollBehavior>[2] = null,
) => routeScrollBehavior(to, from, savedPosition);

describe('routeScrollBehavior', () => {
  it.each([
    ['Home', 'PostDetail'],
    ['Profile', 'PostDetail'],
    ['History', 'PostDetail'],
    ['Notifications', 'PostDetail'],
  ])('starts a fresh PostDetail at the top from %s', (fromName, toName) => {
    expect(applyScrollBehavior(location(toName, { id: '123' }), location(fromName))).toEqual({ top: 0 });
  });

  it('starts a different PostDetail at the top', () => {
    expect(
      applyScrollBehavior(
        location('PostDetail', { id: '456' }),
        location('PostDetail', { id: '123' }),
      ),
    ).toEqual({ top: 0 });
  });

  it.each([
    ['same PostDetail query mutation', location('PostDetail', { id: '123' }, { reply: '1' }), location('PostDetail', { id: '123' })],
    ['reply intent query cleanup', location('PostDetail', { id: '123' }), location('PostDetail', { id: '123' }, { reply: '1' })],
    ['same PostCreate route mutation', location('PostCreate', {}, { draft: '1' }), location('PostCreate')],
  ])('%s does not mutate scroll', (_label, to, from) => {
    expect(applyScrollBehavior(to, from)).toBe(false);
  });

  it.each([
    ['Home', 'PostCreate'],
    ['Profile', 'PostCreate'],
    ['PostDetail', 'PostCreate'],
  ])('starts a fresh PostCreate at the top from %s', (fromName, toName) => {
    expect(applyScrollBehavior(location(toName), location(fromName, { id: '123' }))).toEqual({ top: 0 });
  });

  it.each([
    ['PostDetail', { id: '123' }],
    ['PostCreate', {}],
  ])('returns savedPosition for %s even on a fresh entry', (toName, params) => {
    const savedPosition = { left: 12, top: 2200 };

    expect(applyScrollBehavior(location(toName, params), location('Home'), savedPosition)).toBe(savedPosition);
  });

  it('lets UserProfile own scroll restoration when returning from PostDetail', () => {
    const savedPosition = { left: 12, top: 2200 };

    expect(
      applyScrollBehavior(
        location('UserProfile', { id: '7' }),
        location('PostDetail', { id: '42' }),
        savedPosition,
      ),
    ).toBe(false);
  });

  it('still returns savedPosition when returning from PostDetail to Home', () => {
    const savedPosition = { left: 0, top: 1500 };

    expect(
      applyScrollBehavior(
        location('Home'),
        location('PostDetail', { id: '42' }),
        savedPosition,
      ),
    ).toBe(savedPosition);
  });

  it.each([
    ['Home', 'Search'],
    ['Search', 'Profile'],
    ['Profile', 'Notifications'],
    ['Home', 'CurrencyExchange'],
  ])('does not mutate scroll for unrelated navigation from %s to %s', (fromName, toName) => {
    expect(applyScrollBehavior(location(toName), location(fromName))).toBe(false);
  });
});
