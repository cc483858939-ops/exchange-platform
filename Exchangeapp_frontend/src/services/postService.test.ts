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
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.post.mockResolvedValue({ data: { id: 42, media_url: '/api/files/post-media/42/image.jpg' } });
  });

  it('keeps the existing two-argument request when no option is provided', async () => {
    await createPost({ content: 'hello', media: [] });

    expect(mocks.post).toHaveBeenCalledWith('/posts', { content: 'hello', media: [] });
  });

  it('sends a trimmed Idempotency-Key when provided', async () => {
    await createPost(
      { content: 'hello', media: [] },
      { idempotencyKey: '  00000000-0000-4000-8000-000000000042  ' },
    );

    expect(mocks.post).toHaveBeenCalledWith(
      '/posts',
      { content: 'hello', media: [] },
      { headers: { 'Idempotency-Key': '00000000-0000-4000-8000-000000000042' } },
    );
  });

  it('gives post media uploads the extended timeout', async () => {
    const file = new File(['image'], 'image.png', { type: 'image/png' });

    await expect(uploadPostMedia(file)).resolves.toBe('/api/files/post-media/42/image.jpg');

    expect(mocks.post).toHaveBeenCalledTimes(1);
    const [path, body, config] = mocks.post.mock.calls[0] as [string, FormData, { timeout: number }];
    expect(path).toBe('/uploads/post-media');
    expect(body).toBeInstanceOf(FormData);
    expect(body.get('image')).toBe(file);
    expect(config).toEqual({ timeout: UPLOAD_REQUEST_TIMEOUT_MS });
  });
});
