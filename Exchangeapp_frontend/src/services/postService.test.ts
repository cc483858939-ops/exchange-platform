import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPost } from './postService';

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
    mocks.post.mockResolvedValue({ data: { id: 42 } });
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
});
