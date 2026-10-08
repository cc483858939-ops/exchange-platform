package controllers

// Each actor contributes at most one post and one repost per canonical post.
// An activity beyond an actor's first limit+1 eligible rows has limit+1 distinct
// posts ahead of it, so it cannot represent a post in the global first page.
// Cursor pages use followingTimelineCursorSQL: truncating older activities
// before checking the canonical winner can resurrect posts from earlier pages.
func followingTimelineFirstPageSQL() string {
	return `
WITH follows AS (
    SELECT following_id FROM user_follows WHERE follower_id = ?
), activities AS (
    SELECT 'post'::text AS activity_type,
           posts.created_at AS activity_at,
           posts.id AS source_id,
           posts.id AS post_id,
           posts.author_id AS actor_id,
           1::int AS activity_rank
    FROM follows
    CROSS JOIN LATERAL (
        SELECT posts.id, posts.author_id, posts.created_at
        FROM posts
        WHERE posts.author_id = follows.following_id
          AND posts.reply_to_post_id IS NULL
          AND ` + publicPostEligibilitySQL("posts") + `
        ORDER BY posts.created_at DESC, posts.id DESC
        LIMIT ?
    ) AS posts

    UNION ALL

    SELECT 'repost'::text,
           reposts.created_at,
           reposts.id,
           reposts.post_id,
           reposts.user_id,
           2::int
    FROM follows
    JOIN users AS reposter
      ON reposter.id = follows.following_id
     AND reposter.deleted_at IS NULL
    CROSS JOIN LATERAL (
        SELECT post_reposts.id, post_reposts.created_at,
               post_reposts.post_id, post_reposts.user_id
        FROM post_reposts
        JOIN posts ON posts.id = post_reposts.post_id
        WHERE post_reposts.user_id = follows.following_id
          AND ` + publicPostEligibilitySQL("posts") + `
        ORDER BY post_reposts.created_at DESC, post_reposts.id DESC
        LIMIT ?
    ) AS reposts
), latest AS (
    SELECT DISTINCT ON (post_id)
           activity_type, activity_at, source_id, post_id, actor_id, activity_rank
    FROM activities
    ORDER BY post_id, activity_at DESC, activity_rank DESC, source_id DESC
)
SELECT activity_type, activity_at, source_id, post_id, actor_id, activity_rank
FROM latest
ORDER BY activity_at DESC, activity_rank DESC, source_id DESC
LIMIT ?
`
}

// Reduce reposts to one eligible activity per post before merging them with
// originals. Each side of the full join is unique by post ID, avoiding a second
// historical activity sort. The cursor still filters the global winner.
func followingTimelineCursorSQL() string {
	return `
WITH reposts AS (
    SELECT DISTINCT ON (post_reposts.post_id)
           post_reposts.id, post_reposts.post_id,
           post_reposts.user_id, post_reposts.created_at
    FROM post_reposts
    JOIN user_follows AS repost_follow
      ON repost_follow.following_id = post_reposts.user_id
     AND repost_follow.follower_id = ?
    JOIN users AS reposter
      ON reposter.id = post_reposts.user_id
     AND reposter.deleted_at IS NULL
    JOIN posts ON posts.id = post_reposts.post_id
    WHERE ` + publicPostEligibilitySQL("posts") + `
    ORDER BY post_reposts.post_id, post_reposts.created_at DESC, post_reposts.id DESC
), originals AS (
    SELECT posts.id, posts.author_id, posts.created_at
    FROM posts
    JOIN user_follows AS direct_follow
      ON direct_follow.following_id = posts.author_id
     AND direct_follow.follower_id = ?
    WHERE posts.reply_to_post_id IS NULL
      AND ` + publicPostEligibilitySQL("posts") + `
), matched AS (
    SELECT originals.id AS original_id, originals.author_id,
           originals.created_at AS original_at,
           reposts.id AS repost_id, reposts.user_id,
           reposts.created_at AS repost_at,
           COALESCE(originals.id, reposts.post_id) AS post_id,
           (reposts.id IS NOT NULL AND (
               originals.id IS NULL OR reposts.created_at IS NULL OR
               (originals.created_at IS NOT NULL AND reposts.created_at >= originals.created_at)
           )) AS use_repost
    FROM originals
    FULL JOIN reposts ON reposts.post_id = originals.id
), latest AS (
    SELECT CASE WHEN use_repost THEN 'repost'::text ELSE 'post'::text END AS activity_type,
           CASE WHEN use_repost THEN repost_at ELSE original_at END AS activity_at,
           CASE WHEN use_repost THEN repost_id ELSE original_id END AS source_id,
           post_id,
           CASE WHEN use_repost THEN user_id ELSE author_id END AS actor_id,
           CASE WHEN use_repost THEN 2 ELSE 1 END AS activity_rank
    FROM matched
)
SELECT activity_type, activity_at, source_id, post_id, actor_id, activity_rank
FROM latest
WHERE activity_at < ?
   OR (activity_at = ? AND (activity_rank < ? OR (activity_rank = ? AND source_id < ?)))
ORDER BY activity_at DESC, activity_rank DESC, source_id DESC
LIMIT ?
`
}
