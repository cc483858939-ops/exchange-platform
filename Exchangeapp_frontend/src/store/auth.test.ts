// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('axios', () => ({
  default: {
    create: vi.fn(() => ({ post: mocks.post })),
  },
}));

import { useAuthStore } from './auth';

const fullIdentity = {
  id: 7,
  username: 'alice',
  display_name: 'Alice',
  avatar_url: '/api/files/profile-avatars/7/test.webp',
};

const authResponse = (identity = fullIdentity) => ({
  access_token: 'access-token',
  refresh_token: 'refresh-token',
  token_type: 'Bearer' as const,
  expires_in: 900,
  refresh_expires_in: 604800,
  user: identity,
});

describe('auth store identity persistence', () => {
  beforeEach(() => {
    localStorage.clear();
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('restores legacy storage without logging out', () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 7, username: 'alice' }));

    const store = useAuthStore();

    expect(store.currentIdentity).toEqual({
      id: 7,
      username: 'alice',
      display_name: '',
      avatar_url: '',
    });
    expect(localStorage.getItem('auth_user')).not.toBeNull();
  });

  it('restores a complete stored identity without dropping fields', () => {
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));

    expect(useAuthStore().currentIdentity).toEqual(fullIdentity);
  });

  it('persists the canonical identity returned by login', async () => {
    mocks.post.mockResolvedValueOnce({ data: authResponse() });
    const store = useAuthStore();

    await store.login('alice', 'secret123');

    expect(store.currentIdentity).toEqual(fullIdentity);
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(fullIdentity);
  });

  it('replaces identity state and storage with refresh metadata', async () => {
    localStorage.setItem('token', 'old-access');
    localStorage.setItem('refresh_token', 'old-refresh');
    localStorage.setItem('auth_user', JSON.stringify({
      ...fullIdentity,
      display_name: 'Old',
      avatar_url: '/old.webp',
    }));
    const store = useAuthStore();
    const refreshedIdentity = {
      ...fullIdentity,
      display_name: 'New',
      avatar_url: '/new.webp',
    };
    mocks.post.mockResolvedValueOnce({ data: authResponse(refreshedIdentity) });

    await store.refreshAccessToken();

    expect(store.currentIdentity).toEqual(refreshedIdentity);
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(refreshedIdentity);
  });

  it('synchronizes an own-profile update without touching tokens', () => {
    localStorage.setItem('token', 'access-token');
    localStorage.setItem('refresh_token', 'refresh-token');
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const store = useAuthStore();
    const updated = {
      ...fullIdentity,
      display_name: 'Alice Updated',
      avatar_url: '/new.webp',
    };

    expect(store.syncCurrentIdentityProfile(updated)).toBe(true);
    expect(store.currentIdentity).toEqual(updated);
    expect(JSON.parse(localStorage.getItem('auth_user') || 'null')).toEqual(updated);
    expect(store.token).toBe('Bearer access-token');
    expect(store.refreshToken).toBe('refresh-token');
    expect(localStorage.getItem('token')).toBe('access-token');
    expect(localStorage.getItem('refresh_token')).toBe('refresh-token');
  });

  it('does not let another user replace current identity', () => {
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const store = useAuthStore();
    const storedBefore = localStorage.getItem('auth_user');

    expect(store.syncCurrentIdentityProfile({ ...fullIdentity, id: 8, username: 'bob' })).toBe(false);
    expect(store.currentIdentity).toEqual(fullIdentity);
    expect(localStorage.getItem('auth_user')).toBe(storedBefore);
  });

  it('clears tokens and identity on logout', () => {
    localStorage.setItem('token', 'access-token');
    localStorage.setItem('refresh_token', 'refresh-token');
    localStorage.setItem('auth_user', JSON.stringify(fullIdentity));
    const store = useAuthStore();

    store.logout();

    expect(store.token).toBeNull();
    expect(store.refreshToken).toBeNull();
    expect(store.currentIdentity).toBeNull();
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(localStorage.getItem('auth_user')).toBeNull();
  });
});
