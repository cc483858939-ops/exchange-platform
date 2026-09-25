import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPostReply } from './replyService';

const mocks = vi.hoisted(() => ({
  apiPost: vi.fn(),
  apiGet: vi.fn(),
  apiDelete: vi.fn(),
  createPost: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    get: mocks.apiGet,
    post: mocks.apiPost,
    delete: mocks.apiDelete,
  },
}));

vi.mock('./postService', () => ({
  createPost: mocks.createPost,
}));

describe('replyService createPostReply', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.createPost.mockResolvedValue({ id: 101 });
  });

  it('routes reply creation through createPost with its idempotency options', async () => {
    const options = { idempotencyKey: '00000000-0000-4000-8000-000000000042' };

    await expect(createPostReply('42', 'reply content', options)).resolves.toEqual({ id: 101 });

    expect(mocks.createPost).toHaveBeenCalledWith({
      content: 'reply content',
      reply_to_post_id: 42,
    }, options);
    expect(mocks.apiPost).not.toHaveBeenCalled();
  });
});
