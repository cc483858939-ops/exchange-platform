import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router';
import { describe, expect, it } from 'vitest';
import { resolveAuthSuccessDestination } from './authDestination';

const routes: RouteRecordRaw[] = [
  { path: '/', name: 'Home', component: {} },
  { path: '/notifications', name: 'Notifications', component: {} },
  { path: '/posts/:id', name: 'PostDetail', component: {} },
  { path: '/users/:id', name: 'UserProfile', component: {} },
  { path: '/login', name: 'Login', component: {}, meta: { layout: 'auth' } },
  { path: '/register', name: 'Register', component: {}, meta: { layout: 'auth' } },
];

const router = createRouter({
  history: createMemoryHistory(),
  routes,
});

describe('resolveAuthSuccessDestination', () => {
  it('returns a safe returnTo including its query and hash', () => {
    expect(resolveAuthSuccessDestination(
      router,
      { returnTo: '/posts/42?reply=1#conversation' },
      { id: 7 },
    )).toBe('/posts/42?reply=1#conversation');
  });

  it('prefers a safe returnTo over the profile intent', () => {
    expect(resolveAuthSuccessDestination(
      router,
      { returnTo: '/notifications', intent: 'profile' },
      { id: 7 },
    )).toBe('/notifications');
  });

  it('routes profile intent to a safe numeric identity ID', () => {
    expect(resolveAuthSuccessDestination(router, { intent: 'profile' }, { id: 7 })).toEqual({
      name: 'UserProfile',
      params: { id: '7' },
    });
  });

  it.each([
    'https://evil.example',
    '//evil.example/path',
    '/login',
    '/register',
  ])('falls back to Home for unsafe returnTo %s', (returnTo) => {
    expect(resolveAuthSuccessDestination(router, { returnTo }, { id: 7 })).toEqual({ name: 'Home' });
  });

  it('falls back to Home for an invalid profile identity or unknown intent', () => {
    expect(resolveAuthSuccessDestination(router, { intent: 'profile' }, { id: 0 })).toEqual({ name: 'Home' });
    expect(resolveAuthSuccessDestination(router, { intent: 'settings' }, { id: 7 })).toEqual({ name: 'Home' });
  });
});
