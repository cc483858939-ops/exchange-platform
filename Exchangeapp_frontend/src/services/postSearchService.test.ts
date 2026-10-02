import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('../axios', () => ({ default: { get: mocks.get } }));

import { searchPosts, type PostSearchPage, type PostSearchQuery } from './postSearchService';

describe('postSearchService', () => {
  beforeEach(() => vi.clearAllMocks());

  it('requests the authenticated Post search endpoint with the complete criteria', async () => {
    const page: PostSearchPage = { items: [], next_cursor: 'opaque-next' };
    const query: PostSearchQuery = {
      q: '日元',
      author_id: 42,
      from: '2026-09-01T00:00:00.000Z',
      to: '2026-10-01T00:00:00.000Z',
      sort: 'latest',
      limit: 20,
      cursor: 'opaque-current',
    };
    mocks.get.mockResolvedValue({ data: page });

    await expect(searchPosts(query)).resolves.toEqual(page);
    expect(mocks.get).toHaveBeenCalledExactlyOnceWith('/posts/search', { params: query });
  });
});
