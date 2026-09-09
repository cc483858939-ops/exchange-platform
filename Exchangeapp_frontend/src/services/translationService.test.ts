import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    post: mocks.post,
  },
}));

import { translatePost } from './translationService';

describe('translation service', () => {
  beforeEach(() => {
    mocks.post.mockReset();
  });

  it('posts the exact translation request contract', async () => {
    const response = {
      post_id: 42,
      source_language: 'zh' as const,
      target_language: 'en' as const,
      translated: true,
      translation: 'Hello',
    };
    mocks.post.mockResolvedValue({ data: response });

    await expect(translatePost('42', 'en-US')).resolves.toEqual(response);
    expect(mocks.post).toHaveBeenCalledWith('/posts/42/translation', {
      target_language: 'en',
    });
  });

  it.each(['fr', '', 'en_US'])('rejects unsupported target %s locally', async target => {
    await expect(translatePost(42, target)).rejects.toThrow('Invalid translation target language');
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it.each([0, -1, 1.5, Number.NaN, Number.MAX_SAFE_INTEGER + 1])(
    'rejects invalid post id %s locally',
    async postID => {
      await expect(translatePost(postID, 'en')).rejects.toThrow('Invalid post id');
      expect(mocks.post).not.toHaveBeenCalled();
    },
  );
});
