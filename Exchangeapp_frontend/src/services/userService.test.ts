// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UPLOAD_REQUEST_TIMEOUT_MS } from '../utils/requestTimeout';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  patch: vi.fn(),
}));

vi.mock('../axios', () => ({ default: mocks }));

import { updateUserProfile, uploadProfileAvatar, uploadProfileCover } from './userService';

describe('userService profile cover contract', () => {
  beforeEach(() => {
    mocks.post.mockReset();
    mocks.patch.mockReset();
  });

  it('uploads a cover as image multipart data and returns the canonical URL', async () => {
    mocks.post.mockResolvedValue({ data: { cover_image_url: '/api/files/profile-covers/users/v1/42/hash.jpg' } });
    const file = new File(['cover'], 'cover.png', { type: 'image/png' });

    await expect(uploadProfileCover(file)).resolves.toBe('/api/files/profile-covers/users/v1/42/hash.jpg');

    expect(mocks.post).toHaveBeenCalledTimes(1);
    const [path, body] = mocks.post.mock.calls[0] as [string, FormData];
    expect(path).toBe('/uploads/profile-cover');
    expect(body).toBeInstanceOf(FormData);
    expect(body.get('image')).toBe(file);
    expect(mocks.post).toHaveBeenCalledWith(
      '/uploads/profile-cover',
      body,
      { timeout: UPLOAD_REQUEST_TIMEOUT_MS },
    );
  });

  it('uploads an avatar with the extended timeout', async () => {
    mocks.post.mockResolvedValue({ data: { avatar_url: '/api/files/profile-avatars/users/v1/42/hash.jpg' } });
    const file = new File(['avatar'], 'avatar.png', { type: 'image/png' });

    await expect(uploadProfileAvatar(file)).resolves.toBe('/api/files/profile-avatars/users/v1/42/hash.jpg');

    expect(mocks.post).toHaveBeenCalledTimes(1);
    const [path, body] = mocks.post.mock.calls[0] as [string, FormData];
    expect(path).toBe('/uploads/profile-avatar');
    expect(body).toBeInstanceOf(FormData);
    expect(body.get('image')).toBe(file);
    expect(mocks.post).toHaveBeenCalledWith(
      '/uploads/profile-avatar',
      body,
      { timeout: UPLOAD_REQUEST_TIMEOUT_MS },
    );
  });

  it('accepts cover_image_url in the profile PATCH payload', async () => {
    const profile = { id: 42 } as never;
    mocks.patch.mockResolvedValue({ data: profile });
    const payload = { cover_image_url: '/api/files/profile-covers/users/v1/42/hash.jpg' };

    await updateUserProfile(42, payload);

    expect(mocks.patch).toHaveBeenCalledWith('/users/42', payload);
  });
});
