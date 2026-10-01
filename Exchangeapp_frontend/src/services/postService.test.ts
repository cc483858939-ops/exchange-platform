import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UPLOAD_REQUEST_TIMEOUT_MS } from '../utils/requestTimeout';
import { createPost, uploadPostMedia } from './postService';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    post: mocks.post,
  },
}));

describe('postService createPost', () => {
  const authBinding = Object.freeze({ userID: 7, sessionID: 'session-7', sessionVersion: 5 });

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.post.mockResolvedValue({ data: { id: 42, media_url: '/api/files/post-media/42/image.jpg' } });
  });

  it('passes a required authentication binding when no idempotency key is needed', async () => {
    await createPost({ content: 'hello', media: [] }, { authBinding });

    expect(mocks.post).toHaveBeenCalledWith('/posts', { content: 'hello', media: [] }, {
      _authBinding: authBinding,
    });
  });

  it('sends a trimmed Idempotency-Key when provided', async () => {
    await createPost(
      { content: 'hello', media: [] },
      { idempotencyKey: '  00000000-0000-4000-8000-000000000042  ', authBinding },
    );

    expect(mocks.post).toHaveBeenCalledWith(
      '/posts',
      { content: 'hello', media: [] },
      {
        headers: { 'Idempotency-Key': '00000000-0000-4000-8000-000000000042' },
        _authBinding: authBinding,
      },
    );
  });

  it('gives post media uploads the extended timeout', async () => {
    const file = new File(['image'], 'image.png', { type: 'image/png' });

    await expect(uploadPostMedia(file, { authBinding })).resolves.toBe('/api/files/post-media/42/image.jpg');

    expect(mocks.post).toHaveBeenCalledTimes(1);
    const [path, body, config] = mocks.post.mock.calls[0] as [string, FormData, { timeout: number; _authBinding: typeof authBinding }];
    expect(path).toBe('/uploads/post-media');
    expect(body).toBeInstanceOf(FormData);
    expect(body.get('image')).toBe(file);
    expect(config).toEqual({ timeout: UPLOAD_REQUEST_TIMEOUT_MS, _authBinding: authBinding });
  });
});
