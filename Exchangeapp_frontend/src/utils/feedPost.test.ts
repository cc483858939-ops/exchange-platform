import { describe, expect, it } from 'vitest';
import type { Article } from '../types/Article';
import { articleToFeedPost, followingTimelineItemToFeedPost } from './feedPost';

const canonicalAuthor = {
  id: 9,
  username: 'bob',
  display_name: 'Bob',
  avatar_url: '/bob.png',
};

const article = (overrides: Partial<Article> = {}): Article => ({
  ID: 42,
  CreatedAt: '2026-08-27T00:00:00.000Z',
  UpdatedAt: '2026-08-27T00:00:00.000Z',
  title: 'Canonical post',
  content: 'Canonical body',
  preview: 'Canonical preview',
  cover_image_url: '',
  publication_state: 'published',
  published_at: '2026-08-27T00:00:00.000Z',
  expired_at: null,
  like_count: 4,
  comment_count: 2,
  view_count: 18,
  like_sync_version: 1,
  author: canonicalAuthor,
  ...overrides,
});

const activity = (activityType: 'post' | 'repost') => ({
  activity_type: activityType,
  activity_at: '2026-08-27T01:00:00.000Z',
  source_id: activityType === 'post' ? 42 : 101,
  actor: {
    id: 11,
    username: 'alice',
    display_name: 'Alice',
    avatar_url: '/alice.png',
  },
  article: article(),
});

describe('feed post mapping', () => {
  it('defaults Article repost state to unknown without changing the Article shape', () => {
    const post = articleToFeedPost(article());

    expect(post).toMatchObject({
      id: 42,
      author: canonicalAuthor,
      repostCount: 0,
      reposted: false,
      repostStatus: 'unknown',
    });
    expect(post.repostContext).toBeUndefined();
  });

  it('maps a direct Following activity without context', () => {
    const post = followingTimelineItemToFeedPost(activity('post'));

    expect(post.author).toEqual(canonicalAuthor);
    expect(post.repostContext).toBeUndefined();
  });

  it('maps a repost activity to actor context while preserving the canonical author', () => {
    const post = followingTimelineItemToFeedPost(activity('repost'));

    expect(post.author).toEqual(canonicalAuthor);
    expect(post.repostContext?.actor).toEqual(activity('repost').actor);
  });
});
