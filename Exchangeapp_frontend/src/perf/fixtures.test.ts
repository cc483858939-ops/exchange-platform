import { describe, expect, it } from 'vitest';
import { createPerfPosts } from './fixtures';

describe('performance fixtures', () => {
  it('is deterministic and returns the requested number of unique positive IDs', () => {
    const first = createPerfPosts(300);
    const second = createPerfPosts(300);
    const ids = first.map(post => post.id);

    expect(first).toEqual(second);
    expect(first).toHaveLength(300);
    expect(new Set(ids).size).toBe(300);
    expect(ids.every(id => Number.isSafeInteger(id) && id > 0)).toBe(true);
  });

  it('uses the mixed fixture pattern without remote assets', () => {
    const posts = createPerfPosts(15);

    expect(posts[1].content).toContain('deterministic post');
    expect(posts[2].media[0]?.url).toMatch(/^data:image\/svg\+xml,/);
    expect(posts[4].repostContext?.actor.username).toBe('perf_mina');
    expect(posts[0].likeStatus).toBe('ready');
    expect(posts[0].repostStatus).toBe('ready');
    expect(posts.every(post => post.media.every(item => item.url.startsWith('data:')))).toBe(true);
  });

  it('supports a text-only diagnostic fixture and append-safe IDs', () => {
    const posts = createPerfPosts(3, 'text-only', 301);

    expect(posts.map(post => post.id)).toEqual([301, 302, 303]);
    expect(posts.every(post => post.media.length === 0)).toBe(true);
    expect(posts.every(post => post.repostContext === undefined)).toBe(true);
    expect(createPerfPosts(0)).toEqual([]);
    expect(createPerfPosts(-1)).toEqual([]);
  });
});
