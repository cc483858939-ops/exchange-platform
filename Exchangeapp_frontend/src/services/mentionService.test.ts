import { afterEach, describe, expect, it, vi } from 'vitest';
import { getMentionSuggestions, resolveMentionUsername } from './mentionService';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock('../axios', () => ({ default: { get: mocks.get } }));

describe('mentionService', () => {
  afterEach(() => {
    mocks.get.mockReset();
  });

  it('requests eight ordered suggestions and deduplicates user IDs', async () => {
    const signal = new AbortController().signal;
    mocks.get.mockResolvedValue({ data: { items: [
      { user: { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' }, reason: 'recently_mentioned' },
      { user: { id: 7, username: 'alice', display_name: 'Alice', avatar_url: '' }, reason: 'following' },
      { user: { id: 8, username: 'bob', display_name: 'Bob', avatar_url: '' }, reason: 'following' },
    ] } });

    const result = await getMentionSuggestions('al', signal);

    expect(mocks.get).toHaveBeenCalledWith('/me/mention-suggestions', {
      params: { prefix: 'al', limit: 8 },
      signal,
    });
    expect(result.map(item => item.username)).toEqual(['alice', 'bob']);
    expect(result[0]?.reason).toBe('recently_mentioned');
  });

  it('uses the exact username endpoint only on demand', async () => {
    const signal = new AbortController().signal;
    mocks.get.mockResolvedValue({ data: { id: 2, username: 'alice', display_name: 'Alice' } });

    const result = await resolveMentionUsername('alice', signal);

    expect(mocks.get).toHaveBeenCalledWith('/users/by-username/alice', { signal });
    expect(result?.id).toBe(2);
  });

  it('returns no user for an exact-lookup 404', async () => {
    mocks.get.mockRejectedValue({ isAxiosError: true, response: { status: 404 } });
    await expect(resolveMentionUsername('alice')).resolves.toBeNull();
  });
});
