import { createMemoryHistory, createRouter } from 'vue-router';
import { describe, expect, it } from 'vitest';
import { resolveSafeLoginReturnTarget } from './loginReturnTarget';

const view = {};

const createTestRouter = () => createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'Home', component: view, meta: { layout: 'app' } },
    { path: '/search', name: 'UserSearch', component: view, meta: { layout: 'app' } },
    { path: '/posts/:id', name: 'PostDetail', component: view, meta: { layout: 'app' } },
    { path: '/users/:id/followers', name: 'UserFollowers', component: view, meta: { layout: 'app' } },
    { path: '/notifications', name: 'Notifications', component: view, meta: { layout: 'app' } },
    { path: '/login', name: 'Login', component: view, meta: { layout: 'auth' } },
    { path: '/register', name: 'Register', component: view, meta: { layout: 'auth' } },
  ],
});

describe('resolveSafeLoginReturnTarget', () => {
  it.each([
    ['/', '/'],
    ['/notifications', '/notifications'],
    ['/search?q=alice', '/search?q=alice'],
    ['/posts/42?reply=1#conversation', '/posts/42?reply=1#conversation'],
    ['/users/7/followers', '/users/7/followers'],
  ])('preserves valid application target %s', (candidate, expected) => {
    const router = createTestRouter();

    expect(resolveSafeLoginReturnTarget(router, candidate)).toBe(expected);
  });

  it.each([
    'https://evil.example',
    'http://evil.example',
    '//evil.example',
    '\\\\evil.example',
    '/login',
    '/register',
    '/unregistered-path',
    '',
    undefined,
    null,
    ['/search?q=alice', '/notifications'],
  ])('rejects unsafe or invalid target %#', candidate => {
    const router = createTestRouter();

    expect(resolveSafeLoginReturnTarget(router, candidate)).toBeNull();
  });
});
