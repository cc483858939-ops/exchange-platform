import { describe, expect, it } from 'vitest';
import type { Post } from '../types/Post';
import { postToFeedPost } from './feedPost';

const canonicalAuthor = {
  id: 9,
  username: 'bob',
  display_name: 'Bob',
  avatar_url: '/bob.png',
};

const post = (overrides: Partial<Post> = {}): Post => ({
  id: 42,
  created_at: '2026-08-27T00:00:00.000Z',
  updated_at: '2026-08-27T00:00:00.000Z',
  published_at: '2026-08-27T00:00:00.000Z',
  author: canonicalAuthor,
  content: 'Canonical body',
  conversation_id: 42,
  reply_to_post_id: null,
  quote_post_id: null,
  reply_to_post: null,
  quote_post: null,
  visibility: 'public',
  media: [],
  like_count: 4,
  reply_count: 2,
  view_count: 18,
  deleted: false,
  ...overrides,
});

describe('feed post mapping', () => {
  it('maps a canonical Post and defaults repost state to unknown', () => {
    const feedPost = postToFeedPost(post());

    expect(feedPost).toMatchObject({
      id: 42,
      author: canonicalAuthor,
      content: 'Canonical body',
      media: [],
      replyCount: 2,
      repostCount: 0,
      reposted: false,
      repostStatus: 'unknown',
    });
    expect(feedPost.repostContext).toBeUndefined();
  });

  it('maps repost activity context while preserving the canonical post author', () => {
    const actor = {
      id: 11,
      username: 'alice',
      display_name: 'Alice',
      avatar_url: '/alice.png',
    };
    const feedPost = postToFeedPost(post(), { repostActor: actor });

    expect(feedPost.author).toEqual(canonicalAuthor);
    expect(feedPost.repostContext?.actor).toEqual(actor);
  });
});
