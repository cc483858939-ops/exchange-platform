import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getPostQuotes } from './quoteService';

const mocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('../axios', () => ({ default: { get: mocks.get } }));

describe('quoteService', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('normalizes the Post ID and passes cursor pagination parameters through', async () => {
    const page = { items: [], next_cursor: 'cursor-2' };
    mocks.get.mockResolvedValueOnce({ data: page });

    await expect(getPostQuotes('042', { limit: 20, cursor: 'cursor-1' })).resolves.toBe(page);

    expect(mocks.get).toHaveBeenCalledWith('/posts/42/quotes', {
      params: { limit: 20, cursor: 'cursor-1' },
    });
  });

  it('uses the default query when no options are supplied', async () => {
    mocks.get.mockResolvedValueOnce({ data: { items: [], next_cursor: null } });

    await getPostQuotes(42);

    expect(mocks.get).toHaveBeenCalledWith('/posts/42/quotes', { params: {} });
  });

  it('rejects invalid local Post IDs before making a request', async () => {
    await expect(getPostQuotes('not-a-post')).rejects.toThrow('Invalid post id');
    expect(mocks.get).not.toHaveBeenCalled();
  });
});
