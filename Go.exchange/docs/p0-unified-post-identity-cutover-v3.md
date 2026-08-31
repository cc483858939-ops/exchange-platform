# P0 — Unified Post Identity Cutover

## Direct Agent Execution Spec v3

Status: ready for direct implementation

Repository root: D:\code\mf

Backend: D:\code\mf\Go.exchange

Frontend: D:\code\mf\Exchangeapp_frontend

Frozen baseline:

~~~text
77a6f3ffc4b5842e0d57f5facd9d8e55f8b926ce
~~~

This is a breaking, clean-data cutover. There is no historical content contract to preserve.

This document supersedes the earlier Unified Post Identity Cutover v2. It also supersedes article/comment identity portions of docs/x-like-feed-v2-direct-agent-execution-spec.md. Existing recommendation formulas, feed-selection rules, telemetry guarantees, optimistic mutation semantics, and operational reliability contracts remain authoritative unless this document explicitly changes the content identity they use.

---

# 0. Agent operating contract

Implement the entire cutover. Do not stop after adding models or routes.

The final tree must compile and run as one atomic application version. Intermediate phases are working checkpoints, not deployable compatibility releases.

Do not:

~~~text
add a compatibility Article or Comment API
dual-write Article and Post
backfill legacy content
translate Article IDs to Post IDs
keep old content routes as aliases
redesign recommendation scoring
redesign Kafka delivery or the outbox
add media, search, moderation, private visibility, or new composer products
stage, commit, push, reset, or clean unrelated work
~~~

Before editing:

1. Verify the repository root and current HEAD.
2. Inspect git status and preserve unrelated changes.
3. Read the current CI workflow.
4. Search the complete backend and frontend for all legacy content-identity references listed in section 21.
5. If HEAD differs from the frozen baseline, classify the drift before implementation. Adapt only when the new code does not contradict this contract. Report contradictions instead of silently changing the architecture.

The agent may keep old Go types temporarily during an intermediate checkpoint solely to keep the branch compiling. They must not be migrated, routed, written, or read by the new runtime, and they must be removed before completion.

No intermediate phase may be deployed.

---

# 1. Outcome

Replace the two current content namespaces:

~~~text
Article.ID
Comment.ID
~~~

with one canonical namespace:

~~~text
Post.ID
~~~

The final domain is:

~~~text
short root post      = Post
reply                = Post
reply to a reply     = Post
quote post           = Post
long article         = Post + PostArticle
pure repost          = PostRepost relation
~~~

Post.ID is the only operational content ID in:

~~~text
HTTP APIs
frontend state
Like and Repost
history
Following
recommendation
embedding
behavior
tracking
events
notifications
cache and Redis state
~~~

Article remains a valid name only for long-form metadata and long-form UI concepts such as PostArticle, ArticleCreateView, article cover upload, article title, and article preview.

---

# 2. Explicit non-goals

Do not add:

~~~text
PostMedia
new upload architecture
private or followers-only visibility
content search
home short-post composer UI
quote composer UI
nested conversation tree UI
new reaction types
new ranking weights or formulas
new tracking-token design
new Kafka delivery semantics
new notification types for quote or repost
profile Replies tab
profile Reposts tab
frontend visual redesign
~~~

The backend must support creating short root Posts and quote Posts, but new composer flows for them are deferred.

---

# 3. Frozen decisions

These decisions are not left to the implementing agent.

| Concern | Final decision |
| --- | --- |
| Canonical content ID | Post.ID only |
| JSON naming | snake_case for all new Post contracts |
| Root conversation storage | conversation_id is NULL |
| Root conversation API value | conversation_id equals the root Post ID |
| Reply conversation storage | root Post ID |
| Reply parent | direct parent Post ID |
| Reply count | active direct replies only |
| Quote | a root Post with quote_post_id |
| Pure repost | PostRepost row, never a Post |
| Long article | root Post plus one PostArticle row |
| Article combined with reply or quote | invalid |
| Normal Post publish time | Post.created_at |
| PostArticle publish time | PostArticle.published_at |
| API publish time | derived published_at described in section 6 |
| Profile ordering | derived published_at DESC, Post ID DESC |
| Following direct-post time | derived published_at |
| Recommendation freshness time | derived published_at |
| Reply recommendation signal | effective conversation root Post |
| Nested reply notification recipient | direct parent author |
| Reply notification post_id | newly created reply Post ID |
| Frontend detail surface | generic PostDetail route for every active Post kind |
| Frontend canonical mapper | Post to FeedPost through postToFeedPost only |
| Recommendation candidates | active public non-reply Posts |
| Embeddings | created for every Post kind |
| Runtime schema version | 2 |
| Data migration | none; empty-database schema only |

---

# 4. Canonical database model

## 4.1 Post

Create:

~~~go
type Post struct {
    gorm.Model

    AuthorID uint
    Author   User

    Content string

    ReplyToPostID *uint
    ReplyToPost   *Post

    QuotePostID *uint
    QuotePost   *Post

    ConversationID *uint

    Visibility string

    LikeCount  int64
    ReplyCount int64
    ViewCount  int64

    LikeSyncVersion int64
}
~~~

Table: posts

Required columns and constraints:

~~~text
author_id:
    NOT NULL
    FK users(id)
    ON UPDATE CASCADE
    ON DELETE RESTRICT

content:
    TEXT
    NOT NULL

reply_to_post_id:
    nullable
    FK posts(id)
    ON UPDATE CASCADE
    ON DELETE RESTRICT

quote_post_id:
    nullable
    FK posts(id)
    ON UPDATE CASCADE
    ON DELETE RESTRICT

conversation_id:
    nullable
    FK posts(id)
    ON UPDATE CASCADE
    ON DELETE RESTRICT

visibility:
    NOT NULL
    DEFAULT public
    CHECK visibility = public

like_count:
    NOT NULL
    DEFAULT 0
    CHECK like_count >= 0

reply_count:
    NOT NULL
    DEFAULT 0
    CHECK reply_count >= 0

view_count:
    NOT NULL
    DEFAULT 0
    CHECK view_count >= 0

like_sync_version:
    NOT NULL
    DEFAULT 0
    CHECK like_sync_version >= 0
~~~

Required Post checks:

~~~sql
CHECK (NOT (reply_to_post_id IS NOT NULL AND quote_post_id IS NOT NULL))

CHECK (
    (reply_to_post_id IS NULL AND conversation_id IS NULL)
    OR
    (reply_to_post_id IS NOT NULL AND conversation_id IS NOT NULL)
)
~~~

Required indexes:

~~~text
posts(author_id, created_at DESC, id DESC) WHERE deleted_at IS NULL
posts(reply_to_post_id, created_at DESC, id DESC) WHERE deleted_at IS NULL
posts(conversation_id, created_at DESC, id DESC) WHERE deleted_at IS NULL
posts(quote_post_id) WHERE quote_post_id IS NOT NULL
posts(deleted_at)
~~~

## 4.2 Conversation invariant

Storage:

~~~text
root Post:
    reply_to_post_id = NULL
    conversation_id = NULL

reply:
    reply_to_post_id = direct parent ID
    conversation_id = effective conversation root ID
~~~

When replying to parent:

~~~text
conversation_id =
    parent.conversation_id when non-null
    otherwise parent.id
~~~

Never store root.conversation_id = root.id.

API:

~~~text
effective conversation_id = COALESCE(post.conversation_id, post.id)
~~~

Do not recursively walk ancestors during normal reads.

## 4.3 Post kind invariant

Every active Post is exactly one of:

| Kind | reply_to_post_id | quote_post_id | PostArticle |
| --- | --- | --- | --- |
| short root | NULL | NULL | absent |
| reply | set | NULL | absent |
| quote root | NULL | set | absent |
| long article root | NULL | NULL | present |

Reject every other combination.

In particular:

~~~text
PostArticle plus reply is invalid
PostArticle plus quote is invalid
reply plus quote is invalid
~~~

The cross-table PostArticle exclusivity invariant must be enforced in the create service transaction and covered by integration tests. Do not add a PostgreSQL trigger solely for this P0.

## 4.4 PostArticle

Create:

~~~go
type PostArticle struct {
    PostID uint

    Title         string
    Preview       string
    CoverImageURL string

    PublicationState string
    PublishedAt      *time.Time
    ExpiredAt        *time.Time

    Post Post
}
~~~

Table: post_articles

Constraints:

~~~text
post_id:
    PRIMARY KEY
    FK posts(id)
    ON UPDATE CASCADE
    ON DELETE CASCADE

title:
    NOT NULL

preview:
    NOT NULL

cover_image_url:
    NOT NULL
    existing size/path validation preserved

publication_state:
    NOT NULL
    CHECK publication_state = published

published_at:
    NOT NULL

expired_at:
    nullable
~~~

PostArticle does not duplicate:

~~~text
author_id
content
created_at
like_count
reply_count
view_count
~~~

Long article body is Post.content.

Creation sets one shared UTC now value for Post creation and PostArticle.published_at within the transaction. Preserve the existing cover prefix:

~~~text
/api/files/article-covers/
~~~

Drafts and scheduling are not added.

## 4.5 Engagement and recommendation tables

Create the following final table names:

~~~text
post_reaction
post_reposts
post_embeddings
post_behaviors
user_post_reco_states
~~~

PostReaction preserves the current ArticleReaction state machine and fields, replacing article_id with post_id. Its primary key is:

~~~text
(user_id, post_id)
~~~

Keep:

~~~text
reaction
liked
reaction_version
updated_at
state_changed_at
~~~

Rename ArticleReactionLike to PostReactionLike.

PostRepost:

~~~go
type PostRepost struct {
    ID        uint
    UserID    uint
    PostID    uint
    CreatedAt time.Time
}
~~~

Required PostRepost constraints:

~~~text
UNIQUE(user_id, post_id)
INDEX(user_id, created_at DESC, id DESC)
INDEX(post_id)
FK user_id -> users(id) ON UPDATE CASCADE ON DELETE CASCADE
FK post_id -> posts(id) ON UPDATE CASCADE ON DELETE CASCADE
~~~

Undo repost hard-deletes the relation.

PostReaction has FKs from user_id to users and post_id to posts, both ON UPDATE CASCADE and ON DELETE CASCADE.

PostEmbedding preserves all current vector, version, model, dimensions, content hash, and timestamp semantics. Its primary key is post_id. Its Post FK is ON UPDATE CASCADE and ON DELETE CASCADE.

PostBehavior preserves all current action, count, active, timestamp, behavior-version, uniqueness, and decay semantics. Replace article_id with post_id only. Its User and Post FKs are ON UPDATE CASCADE and ON DELETE CASCADE.

UserPostRecoState preserves all current projection fields and canonical-version semantics. Its User and Post FKs are ON UPDATE CASCADE and ON DELETE CASCADE. Its key is:

~~~text
(user_id, post_id)
~~~

RecommendationResultTrace and RecommendationDailyMetric keep their type and table names, but every ArticleID field and article_id column inside them becomes PostID and post_id. Rebuild their Post-based indexes, uniqueness constraints, and primary keys.

Do not rename UserRecoProfile, UserAuthorAffinity, UserRecoProfileDirty, or RecommendationRequest when they do not store a content ID.

---

# 5. Creation and mutation invariants

All user-supplied content is trimmed and must remain non-empty.

The client cannot supply:

~~~text
author_id
conversation_id
visibility
publication_state
published_at
like_count
reply_count
view_count
~~~

The server supplies the authenticated author and public visibility.

Reply and quote targets must:

~~~text
exist
be active
be publicly readable under the shared Post eligibility rules
have an active author
~~~

Self-reply and self-quote are allowed.

Reply to reply is allowed.

Quote content must be non-empty. Empty quote content is not a repost.

Lock the direct parent row during reply creation so validation, reply insertion, and direct-parent reply_count increment are atomic with concurrent delete/create operations.

## 5.1 Reply transaction

For a reply R to direct parent P:

~~~text
BEGIN
load and lock P using active public eligibility
derive root = COALESCE(P.conversation_id, P.id)
insert R with reply_to_post_id=P.id and conversation_id=root
increment P.reply_count exactly once
UPSERT PostBehavior(user=actor, post_id=root, action=reply)
update UserPostRecoState reply signal for root
invalidate the actor recommendation profile
write post.reply.created to outbox
COMMIT
after commit, best-effort initialize Redis Like state
after commit, best-effort publish post.embedding.requested when embedding is enabled
~~~

The reply recommendation signal belongs to the effective conversation root, not the new reply and not the direct parent when the parent is itself a reply. This preserves the current article-level reply-interest meaning.

The reply Post still receives its own embedding so later Likes or reads of that reply can contribute Post-specific signals.

Embedding request delivery and Redis Like initialization preserve the current Article-create availability contract: their failure is recorded and observable but does not roll back an already committed Post or change HTTP 201 to failure. Do not move embedding requests into the outbox as part of this identity cutover.

## 5.2 Other behavior identity

Like, unlike, view, click, read, dwell, and not-interested behavior targets the exact Post the user interacted with.

Only reply creation is normalized to the effective conversation root for its reply-interest signal.

---

# 6. Canonical time contract

The old implementation uses articles.published_at for profile cursors, Following activity, recall ordering, and recommendation freshness. The unified model must retain one explicit equivalent.

Define:

~~~text
effective_published_at(post) =
    PostArticle.published_at when a PostArticle row exists
    otherwise Post.created_at
~~~

In SQL, use a shared helper/expression based on a LEFT JOIN to post_articles. Do not independently rewrite the expression in each controller.

PostArticle eligibility still requires a non-null, non-future published_at. Do not use Post.created_at as a fallback for a malformed PostArticle row.

Use effective_published_at for:

~~~text
postResponse.published_at
profile ordering and cursor
direct-post Following activity_at
recommendation recent recall
recommendation following recall ordering
recommendation semantic recent-window split
recommendation trending cutoff and decay
recommendation score tie-breaks
exploration age rules
~~~

Use Post.created_at for:

~~~text
direct reply ordering
Post row audit timestamps
~~~

Use PostRepost.created_at for repost activity_at.

All comparison values and API timestamps are UTC.

---

# 7. Public eligibility and loaders

Create one shared Post eligibility implementation used by detail, profile, Following, history, recommendation hydration, and notification visibility.

A non-article Post is publicly readable when:

~~~text
posts.deleted_at IS NULL
posts.visibility = public
author exists and is not deleted
~~~

A PostArticle Post must additionally satisfy:

~~~text
post_articles.publication_state = published
post_articles.published_at IS NOT NULL
post_articles.published_at <= now
post_articles.expired_at IS NULL OR post_articles.expired_at > now
~~~

An absent PostArticle row means the Post is a normal Post. A malformed existing PostArticle row must not make the Post fall back to normal-Post eligibility.

Provide two distinct loader modes:

~~~text
normal public loader:
    active public Post only

tombstone reference loader:
    may read a soft-deleted Post unscoped
    returns only ID and deleted=true for deleted or no-longer-public targets
~~~

Never expose deleted content or deleted author data through a bounded reference.

---

# 8. Canonical HTTP response

All new Post JSON uses snake_case. Do not preserve the current mixed ID, CreatedAt, and created_at Article shapes.

## 8.1 Post response

~~~json
{
  "id": 123,
  "created_at": "2026-08-31T00:00:00Z",
  "updated_at": "2026-08-31T00:00:00Z",
  "published_at": "2026-08-31T00:00:00Z",
  "author": {
    "id": 7,
    "username": "alice",
    "display_name": "Alice",
    "avatar_url": ""
  },
  "content": "text",
  "conversation_id": 123,
  "reply_to_post_id": null,
  "quote_post_id": null,
  "reply_to_post": null,
  "quote_post": null,
  "visibility": "public",
  "article": null,
  "like_count": 0,
  "reply_count": 0,
  "view_count": 0,
  "deleted": false
}
~~~

Post response fields are viewer independent. Do not add liked, reposted, or viewer_state.

PostArticle response:

~~~json
{
  "title": "Title",
  "preview": "Preview",
  "cover_image_url": "/api/files/article-covers/file",
  "publication_state": "published",
  "published_at": "2026-08-31T00:00:00Z",
  "expired_at": null
}
~~~

The top-level published_at is the effective time from section 6. The nested article published_at remains the long-article publication field.

## 8.2 Bounded references

Active reference:

~~~json
{
  "id": 122,
  "author": {
    "id": 8,
    "username": "bob",
    "display_name": "Bob",
    "avatar_url": ""
  },
  "content": "referenced text",
  "published_at": "2026-08-31T00:00:00Z",
  "article": null,
  "deleted": false
}
~~~

Deleted or no-longer-public reference:

~~~json
{
  "id": 122,
  "deleted": true
}
~~~

Do not recursively include reply_to_post or quote_post inside a reference.

Normal response behavior:

~~~text
reply Post:
    reply_to_post contains the bounded direct-parent reference

quote Post:
    quote_post contains the bounded quoted reference

root short or article Post:
    both references are null
~~~

---

# 9. Canonical APIs

## 9.1 Core routes

Final routes:

~~~text
POST   /api/posts
GET    /api/posts/:id
DELETE /api/posts/:id
GET    /api/posts/:id/replies

POST   /api/posts/like-states
GET    /api/posts/:id/like
PUT    /api/posts/:id/like
DELETE /api/posts/:id/like

POST   /api/posts/repost-states
GET    /api/posts/:id/repost
PUT    /api/posts/:id/repost
DELETE /api/posts/:id/repost

GET    /api/users/:id/posts
GET    /api/feed/following
GET    /api/recommendations/posts

POST   /api/post-view-events
POST   /api/recommendation-events

GET    /api/me/history/likes
GET    /api/me/notifications
~~~

Remove:

~~~text
/api/articles/*
/api/comments/*
/api/users/:id/articles
/api/recommendations/articles
/api/article-view-events
~~~

The article-cover upload route may remain article-named.

## 9.2 Create Post

Short root:

~~~json
{
  "content": "hello",
  "reply_to_post_id": null,
  "quote_post_id": null,
  "article": null
}
~~~

Reply:

~~~json
{
  "content": "reply",
  "reply_to_post_id": 123
}
~~~

Quote:

~~~json
{
  "content": "my comment",
  "quote_post_id": 123
}
~~~

Long article:

~~~json
{
  "content": "article body",
  "article": {
    "title": "title",
    "preview": "preview",
    "cover_image_url": "/api/files/article-covers/file",
    "expired_at": null
  }
}
~~~

Return HTTP 201 and the canonical Post response.

Status contract:

~~~text
400 malformed JSON, invalid ID, empty content, or invalid Post-kind combination
401 missing or inactive authenticated user
404 reply or quote target unavailable
500 persistence or required outbox failure
~~~

Optional embedding publication follows current availability semantics. Required reply activity outbox creation remains transactional and fail-closed.

## 9.3 Get and delete

GET returns 200 with canonical Post or 404 for deleted/non-public/unavailable content.

DELETE:

~~~text
401 missing/inactive user
403 active Post exists but caller is not author
404 Post missing or already deleted
204 first successful owner delete
~~~

A repeated delete must not decrement counters or repeat cleanup.

## 9.4 Direct replies page

GET /api/posts/:id/replies returns only active direct children:

~~~json
{
  "items": [],
  "next_cursor": null
}
~~~

Order:

~~~text
posts.created_at DESC
posts.id DESC
~~~

Default limit: 20

Maximum limit: 50

Opaque cursor payload before base64url encoding:

~~~json
{
  "v": 1,
  "created_at": "2026-08-31T00:00:00Z",
  "id": 123
}
~~~

The route works for every active Post kind, including a reply Post. It does not recursively serialize descendants.

## 9.5 Like state

Bulk request:

~~~json
{
  "post_ids": [1, 2]
}
~~~

Bulk response:

~~~json
{
  "items": [
    {
      "post_id": 1,
      "likes": 3,
      "liked": true
    }
  ],
  "unavailable_post_ids": []
}
~~~

Preserve the current maximum of 100 IDs and all Redis/PostgreSQL version, claim, snapshot, recovery, idempotency, and tombstone semantics.

Likes work on roots, replies, quotes, and PostArticle Posts.

## 9.6 Repost state

Use post_ids and post_id in every request and response.

Repost any active public Post, including a reply.

PUT is idempotent and creates at most one active relation.

DELETE hard-deletes the relation.

Do not denormalize repost_count onto posts.

---

# 10. Counters and deletion

Post.reply_count is the count of active direct replies only.

Create a reply:

~~~text
direct parent reply_count +1 exactly once
conversation root reply_count unchanged unless it is the direct parent
~~~

Delete a reply:

~~~text
direct parent reply_count -1 exactly once
never below zero
~~~

All Post deletion is soft deletion.

Deleting a Post:

~~~text
soft-deletes only that Post
does not delete descendants
does not delete quoting Posts
hard-deletes active PostRepost rows targeting the deleted Post
invalidates Post detail cache
removes active Redis Like keys for the deleted Post
excludes the Post from normal public surfaces
preserves structural reply and quote FKs
~~~

Keep relational PostReaction, PostEmbedding, PostBehavior, UserPostRecoState, and PostArticle rows as historical/rebuildable data. Public loaders and recommendation eligibility must exclude the deleted Post.

For:

~~~text
P1 root
P2 replies to P1
P3 replies to P2
P4 quotes P1
~~~

after deleting P1:

~~~text
P1 is a tombstone
P2 remains active
P3 remains active
P4 remains active
P2 reply_to_post resolves to {id:P1, deleted:true}
P4 quote_post resolves to {id:P1, deleted:true}
~~~

Never expose P1 content through a stale cache or reference.

---

# 11. Profile, Following, history, and recommendation

## 11.1 Profile

Replace GET /api/users/:id/articles with GET /api/users/:id/posts.

Include authored:

~~~text
short roots
quote roots
PostArticle roots
~~~

Exclude:

~~~text
reply Posts
pure repost activity
~~~

P0 deliberately preserves the current authored-content profile semantics. A future profile activity endpoint may add Replies and Reposts without another identity migration.

Order:

~~~text
effective_published_at DESC
posts.id DESC
~~~

Cursor:

~~~json
{
  "v": 2,
  "published_at": "2026-08-31T00:00:00Z",
  "id": 123
}
~~~

Return a page of canonical Post responses.

## 11.2 Following timeline

Keep the activity envelope:

~~~json
{
  "activity_type": "repost",
  "activity_at": "2026-08-31T00:00:00Z",
  "source_id": 500,
  "actor": {},
  "post": {}
}
~~~

Direct authored activity:

~~~text
activity_type = post
activity_at = effective_published_at
source_id = post.id
actor = post.author
post must be non-reply
~~~

Repost activity:

~~~text
activity_type = repost
activity_at = post_reposts.created_at
source_id = post_reposts.id
actor = reposter
post.author = canonical Post author
~~~

A reply does not appear merely because its author was followed. A followed user's repost of a reply may appear because it is a repost activity.

Preserve the current deduplication contract:

~~~text
combine direct-post and repost candidates
select the newest activity per post_id
tie-break per post_id by activity_rank DESC, source_id DESC
activity_rank: repost=2, post=1
order page by activity_at DESC, activity_rank DESC, source_id DESC
~~~

Preserve the opaque cursor fields:

~~~text
activity_at
activity_type
source_id
~~~

Preserve the current fallback behavior: if the latest repost activity stops being eligible, an eligible direct-post activity for the same Post may become visible.

## 11.3 Like history

GET /api/me/history/likes keeps its route but returns canonical Posts.

It may include active public roots, replies, quotes, and PostArticle Posts that the viewer currently Likes.

Preserve current pagination and state hydration behavior, changing only Article identity to Post identity.

## 11.4 Recommendation

Replace GET /api/recommendations/articles with GET /api/recommendations/posts.

Response item:

~~~json
{
  "post": {},
  "score": 1.0,
  "tracking": {
    "request_id": "",
    "position": 1,
    "scene": "",
    "ranker_version": "",
    "ranker_config_hash": "",
    "strategy_id": "",
    "token": "",
    "expires_at": ""
  }
}
~~~

Do not create a duplicate RecommendedPost content DTO. The post field is the canonical Post response.

Candidate eligibility:

~~~text
active public Post
reply_to_post_id IS NULL
not self-authored under the existing rule
not excluded by current interaction/served rules
PostArticle publication/expiration eligibility when extension exists
~~~

Eligible:

~~~text
short root
quote root
PostArticle root
~~~

Excluded:

~~~text
reply
deleted/unavailable Post
expired, future, draft, or malformed PostArticle
~~~

Every current query or function using Article.published_at must use effective_published_at from section 6.

Preserve without numerical change:

~~~text
semantic recall
following recall
recent recall
trending recall
negative signals
served hard and soft exclusion
self-authored exclusion
network balance
novel-author exploration
author diversity
semantic duplicate penalty
ranker and strategy versions
tracking-token verification
request and result trace behavior
performance limits
~~~

Equivalent Post fixtures must produce equivalent candidate eligibility, scores, tie-breaks, and selection outcomes to the current Article fixtures.

Embedding input:

~~~text
Post.content
plus PostArticle.title and PostArticle.preview when the extension exists
~~~

Do not include quoted target content in a quote Post embedding. That avoids transitive content hashes and delete-driven re-embedding.

Create or request an embedding for every Post kind. Replies remain excluded from candidate serving but their embeddings may contribute to a user's interaction profile.

---

# 12. Cache and Redis cutover

## 12.1 Post detail cache

Final namespace:

~~~text
post:detail:v1:{post_id}
~~~

Do not read article:detail keys.

Cache only viewer-independent base Post data. Hydrate reply/quote bounded references after the cache read.

This is mandatory so deleting a referenced Post cannot expose its old content until TTL expiry.

Do not cache liked, reposted, or any viewer state.

## 12.2 Like Redis keys

Final keys:

~~~text
post:likes:dirty
post:likes:processing
post:likes:claims

post:likes:behavior:dirty
post:likes:behavior:state
post:likes:behavior:processing
post:likes:behavior:claims

post:like:{post_id}:ready
post:like:{post_id}:count
post:like:{post_id}:users
post:like:{post_id}:version
~~~

Rename internal variables and Lua script identifiers from article_id to post_id without changing claim or retry behavior.

Do not read old article:* Like keys.

Because this is a clean-data cutover, deployment requires an operator-controlled Redis reset. The implementation agent must document the exact reset requirement but must not destroy a developer's Redis data or Docker volume without explicit approval.

---

# 13. Events, outbox, and Kafka

Keep:

~~~text
outbox_events
consumer_inbox
Debezium outbox routing
transaction boundaries
retry and DLQ behavior
idempotency
event ID guarantees
partitioning intent
~~~

Do not capture posts directly with Debezium.

## 13.1 Final event types

Use:

~~~text
post.viewed
post.liked
post.unliked
post.like.snapshot
post.embedding.requested
post.reaction.applied
post.reply.created

recommendation.impression
recommendation.click
recommendation.read_end
recommendation.feed_dwell
recommendation.not_interested

user_follow.created
~~~

Remove Article and Comment event type constants from executable code.

New post.* event names start at envelope schema_version 1.

Recommendation event names do not change, but their payload changes from article_id to post_id. Therefore bump RecommendationBehaviorSchemaVersion from 2 to 3. Consumers must accept the new version required by this atomic cutover; do not silently accept the old payload.

## 13.2 Final payloads

User behavior:

~~~json
{
  "user_id": 7,
  "post_id": 123,
  "action": "view",
  "source": "post_detail",
  "like_version": 0
}
~~~

Allowed view sources become:

~~~text
feed
post_detail
~~~

Post embedding request:

~~~json
{
  "post_id": 123
}
~~~

Post reaction activity:

~~~json
{
  "actor_id": 7,
  "post_id": 123,
  "post_author_id": 8,
  "liked": true,
  "reaction_version": 3,
  "state_changed_at": "2026-08-31T00:00:00Z"
}
~~~

Post reply activity:

~~~json
{
  "reply_post_id": 124,
  "parent_post_id": 123,
  "conversation_id": 100,
  "actor_id": 7,
  "parent_author_id": 8,
  "created_at": "2026-08-31T00:00:00Z"
}
~~~

Recommendation behavior keeps every existing field and replaces article_id with post_id.

## 13.3 Envelope and partition rules

Preserve current user-oriented partitioning for view and recommendation telemetry.

Use:

~~~text
post.viewed:
    aggregate_type=user
    aggregate_id=user_id
    partition key=user_id

post.liked/post.unliked behavior:
    aggregate_type=post
    aggregate_id=post_id
    partition key=user_id:post_id

post.like.snapshot:
    aggregate_type=post
    aggregate_id=post_id
    partition key=post_id

post.embedding.requested:
    aggregate_type=post
    aggregate_id=post_id
    partition key=post_id

post.reaction.applied:
    aggregate_type=post_reaction
    aggregate_id=actor_id:post_id
    partition key=actor_id:post_id

post.reply.created:
    aggregate_type=post
    aggregate_id=reply_post_id
    partition key=conversation_id
~~~

The reply partition key keeps one conversation ordered without recursively loading ancestors.

## 13.4 Topic and configuration cutover

Keep generic physical topics:

~~~text
goexchange.user.behavior.v1
goexchange.recommendation.events.v1
goexchange.activity.events.v1
goexchange.notification.projection.dlq.v1
~~~

Rename Article-specific physical topics:

~~~text
goexchange.article.like.snapshot.v1
    -> goexchange.post.like.snapshot.v1

goexchange.article.embedding.v1
    -> goexchange.post.embedding.v1
~~~

Final configuration:

~~~text
like_snapshot_topic: goexchange.post.like.snapshot.v1
post_embedding_topic: goexchange.post.embedding.v1
post_embedding_group_id: goexchange-post-embedding-v1
~~~

Rename ArticleEmbeddingTopic and ArticleEmbeddingGroupID configuration fields to PostEmbeddingTopic and PostEmbeddingGroupID. Update config, required-topic initialization, Compose, Kubernetes, tests, workers, and commands in the same cutover.

Producer and consumer changes land together.

Because no event history is retained, no mixed-schema topic compatibility is required. Deployment must start with empty/reset content-event topics, empty outbox/inbox state, or new topic names as specified.

---

# 14. Notifications

Final Notification content reference:

~~~go
PostID *uint
~~~

Remove ArticleID and CommentID fields and columns.

Keep:

~~~text
post_liked
post_replied
user_followed
dedupe_key
source_version
activity_at
read_at
~~~

Constraints:

~~~text
post_liked:
    post_id NOT NULL
    source_version > 0

post_replied:
    post_id NOT NULL
    source_version = 0

user_followed:
    post_id NULL
    source_version > 0
~~~

Notification Post FK:

~~~text
post_id -> posts(id)
ON UPDATE CASCADE
ON DELETE RESTRICT
~~~

Semantics:

~~~text
post_liked:
    recipient = exact liked Post author
    post_id = liked Post ID
    suppress self-like
    dedupe_key = post_like:{actor_id}:{post_id}
    source_version = reaction version

post_replied:
    recipient = direct parent Post author
    post_id = newly created reply Post ID
    suppress when actor is direct parent author
    dedupe_key = post_reply:{reply_post_id}
    source_version = 0

user_followed:
    unchanged
~~~

For a reply to a reply, notify the direct parent's author, not automatically the conversation-root author.

Notification response:

~~~json
{
  "id": 1,
  "type": "post_replied",
  "actor": {},
  "post_id": 124,
  "conversation_id": 100,
  "activity_at": "2026-08-31T00:00:00Z",
  "read": false
}
~~~

conversation_id is required for Post notifications and null for user_followed. Derive it from the notification Post using the effective conversation rule.

The shared notification visibility query must:

~~~text
filter recipient and actor validity
join Post through post_id when non-null
apply active public Post eligibility before ordering and limit
validate notification-type shape
~~~

A notification whose Post is deleted or unavailable is hidden.

Reprocessing an activity event must remain idempotent.

---

# 15. Frontend cutover

## 15.1 Canonical types

Create one API Post type:

~~~ts
export interface Post {
  id: number
  created_at: string
  updated_at: string
  published_at: string

  author: PublicAuthor
  content: string

  conversation_id: number
  reply_to_post_id: number | null
  quote_post_id: number | null

  reply_to_post: PostReference | null
  quote_post: PostReference | null

  visibility: 'public'
  article: PostArticle | null

  like_count: number
  reply_count: number
  view_count: number
  deleted: false
}
~~~

PostArticle:

~~~ts
export interface PostArticle {
  title: string
  preview: string
  cover_image_url: string
  publication_state: 'published'
  published_at: string
  expired_at: string | null
}
~~~

PostReference is a discriminated union:

~~~ts
export type PostReference =
  | {
      id: number
      deleted: true
    }
  | {
      id: number
      author: PublicAuthor
      content: string
      published_at: string
      article: Pick<PostArticle, 'title' | 'preview' | 'cover_image_url'> | null
      deleted: false
    }
~~~

Delete generic canonical Article and Comment API types after consumers migrate. True long-form draft types may remain article-named.

## 15.2 One presentation mapper

FeedPost may remain presentation state.

There is exactly one canonical mapper:

~~~text
Post -> postToFeedPost() -> FeedPost
~~~

The mapper uses:

~~~text
id = post.id
author = post.author
createdAt = post.published_at
title = post.article?.title or empty
excerpt = post.article?.preview when present, otherwise post.content
coverImageUrl = post.article?.cover_image_url or empty
likeCount = post.like_count
replyCount = post.reply_count
viewCount = post.view_count
quoteReference = post.quote_post
~~~

Following and recommendation wrappers may add presentation context after calling postToFeedPost, but may not independently remap canonical content fields.

Remove:

~~~text
articleToFeedPost
recommendationToFeedPost
followingTimelineItemToFeedPost as a content mapper
~~~

## 15.3 Viewer state

Keep Like/Repost viewer state outside the canonical Post:

~~~text
liked
likeStatus
reposted
repostStatus
repostCount
~~~

Rename mutation identity throughout:

~~~text
articleId -> postId
commentId -> postId when it identifies reply content
commentCount -> replyCount
~~~

Preserve optimistic concurrency, stale-hydration protection, cross-surface synchronization, route-entry lifecycle, logout/user-switch safety, and unavailable state behavior.

## 15.4 Generic Post detail surface

Create the canonical frontend route:

~~~text
path: /posts/:id
name: PostDetail
component: PostDetailView
~~~

Remove the canonical content route /news/:id. Keep /news/new for the true long-article creation UI.

Every PostCard content, reply, view, notification, and copy-link action routes to PostDetail.

PostDetailView must load GET /api/posts/:id and support:

~~~text
short root Post
reply Post
quote Post
PostArticle Post
~~~

PostArticle variant:

~~~text
preserve the current NewsDetail visual layout
render body from post.content
render title, preview, cover, and expiration metadata from post.article
retain current article read tracking behavior
~~~

Short or quote variant:

~~~text
render author, content, quote reference when present, engagement, and direct replies
do not run long-article read geometry
~~~

Reply variant:

~~~text
render bounded direct-parent reference
render reply content and engagement
render its own direct replies
~~~

All active Post kinds expose the existing flat reply composer/list behavior using:

~~~text
POST /api/posts
GET /api/posts/:id/replies
DELETE /api/posts/:reply_id
~~~

No nested tree UI is required.

The implementation may reuse and rename NewsDetailView rather than redesigning its CSS, but the final behavior and route must be generic.

## 15.5 PostCard

Render:

~~~text
short:
    author, content, engagement

PostArticle:
    author, title, preview/content, cover, engagement

quote:
    own content plus one bounded reference card

repost activity:
    reposter context plus canonical original Post
~~~

Deleted reference renders a tombstone only.

Repost actor remains timeline/presentation context and never becomes Post.author.

## 15.6 Existing product surfaces

ArticleCreateView remains the long-article editor. It submits POST /api/posts with the article object.

Home, Following, Profile, History, Notifications, and recommendation all consume canonical Post.

Rename the Article view telemetry client, queue item fields, and session-storage key to a Post version. Do not read or translate a persisted queue containing article_id; the clean cutover starts a new queue namespace.

Notification navigation:

~~~text
user_followed -> UserProfile
post_liked -> PostDetail(notification.post_id)
post_replied -> PostDetail(notification.post_id)
~~~

The reply detail uses conversation_id for context but navigation targets the reply Post itself.

No short-post or quote composer is added.

---

# 16. Migration and runtime schema

This is a clean-database schema, not an in-place migration.

Set:

~~~text
RequiredSchemaVersion = 2
~~~

RunMigrations must create only the final active content schema. Remove or stop executing legacy Article/Comment ALTER statements that assume old tables exist.

Final active content tables:

~~~text
posts
post_articles
post_reaction
post_reposts
post_embeddings
post_behaviors
user_post_reco_states
~~~

Final supporting tables such as users, follows, outbox, inbox, recommendation requests/traces/metrics, profiles, affinities, notifications, and runtime schema state remain, with content-ID columns migrated to post_id where required.

The empty database must not contain:

~~~text
articles
comments
article_reaction
article_reposts
article_embeddings
article_behaviors
user_article_reco_states
notification.article_id
notification.comment_id
~~~

Runtime canaries must verify:

~~~text
schema version 2
all required Post tables and columns
Post FKs/checks/indexes
notification.post_id
recommendation trace/metric post_id
absence of required legacy runtime columns
~~~

Document that deployment requires a PostgreSQL content-schema reset, a Redis Like-state reset, and clean/new content-event topics. Do not automatically delete a developer database, Redis volume, Kafka volume, or Docker volume without explicit approval.

---

# 17. Implementation sequence

Execute in this order. The sequence minimizes broken seams, but the result is one atomic cutover.

## Phase A — inventory and contract tests

1. Record all legacy identity hits.
2. Add focused failing tests for the final Post graph, JSON shape, effective time, generic detail behavior, and event payloads.
3. Confirm current ranking fixtures and state-machine tests that must remain numerically equivalent.

Gate:

~~~text
the change manifest is complete
new tests fail for the expected missing Post implementation
no production behavior changed yet
~~~

## Phase B — schema and core Post vertical slice

Implement:

~~~text
Post and PostArticle
shared eligibility and effective time
canonical response and bounded references
create/get/delete/replies
reply transactions and counters
cache base record and reference hydration
runtime schema v2
clean-database migration tests
~~~

Legacy types may remain compile-only during this phase. They must not be in final RunMigrations.

Gate:

~~~text
clean empty-database migration passes
Post graph, counter, kind validation, deletion, and cache tests pass
backend compiles
~~~

## Phase C — engagement

Migrate:

~~~text
PostReaction
PostRepost
Like Redis keys and Lua/store identifiers
Like snapshots
Like history
view counts
PostBehavior
UserPostRecoState
~~~

Gate:

~~~text
Like and Repost state-machine/idempotency/recovery tests pass
reply Like and repost tests pass
old Redis namespaces are not read
~~~

## Phase D — events and notifications

Migrate:

~~~text
event types and payloads
recommendation payload schema version 3
embedding producer/consumer
activity outbox
notification projection and visibility
topic configuration and required-topic initialization
commands and workers
~~~

Gate:

~~~text
producer/consumer contract tests pass
outbox rollback and inbox idempotency tests pass
nested-reply recipient tests pass
no mixed Article/Post payload is accepted
~~~

## Phase E — query surfaces and recommendation

Migrate:

~~~text
Profile
Following
History
recommendation recall, ranking hydration, tracking, traces, metrics, and profiles
~~~

Gate:

~~~text
effective-time tests pass on short, quote, and PostArticle fixtures
Following cursor/dedupe/fallback tests pass
equivalent recommendation fixtures preserve scores and selection
all recommendation performance assertions remain unchanged
~~~

## Phase F — frontend

Implement:

~~~text
Post types and service
postToFeedPost
PostDetail route/view
PostCard variants and bounded reference
ArticleCreate submission
reply flow
Like/Repost hydration
Home/Profile/History/Notifications/recommendation migration
telemetry post_id and post_detail source
~~~

Gate:

~~~text
frontend tests pass
production build passes
all Post kinds have a working detail route
one canonical mapper remains
~~~

## Phase G — legacy removal and whole-tree closure

Remove:

~~~text
models.Article as canonical root
models.Comment
ArticleReaction
ArticleRepost
ArticleEmbedding
ArticleBehavior
UserArticleRecoState
old content controllers and DTOs
old content routes
old frontend Article/Comment canonical services and types
obsolete one-off migration/backfill commands
~~~

Retain genuine PostArticle concepts.

Gate:

~~~text
completion grep is classified
backend unit/integration/static checks pass
frontend tests/build pass
container builds required by CI pass
final acceptance scenario passes
~~~

---

# 18. Required tests

Existing tests may be renamed or replaced only by equivalent or stronger Post tests.

## 18.1 Post kind validation

Assert:

~~~text
short root accepted
reply accepted
quote accepted
PostArticle accepted
reply plus quote rejected
PostArticle plus reply rejected
PostArticle plus quote rejected
empty quote rejected
unavailable target rejected
~~~

## 18.2 Graph and conversation

Create:

~~~text
A creates P1 root
B creates P2 replying to P1
A creates P3 replying to P2
B creates P4 quoting P1
B reposts P1
~~~

Storage:

~~~text
P1 reply_to=NULL conversation=NULL
P2 reply_to=P1 conversation=P1
P3 reply_to=P2 conversation=P1
P4 quote=P1 conversation=NULL
~~~

API:

~~~text
P1 conversation_id=P1
P2 conversation_id=P1
P3 conversation_id=P1
P4 conversation_id=P4
~~~

## 18.3 Counter concurrency and delete graph

Prove:

~~~text
direct reply increments once
nested reply increments only direct parent
successful first delete decrements once
repeated/concurrent delete does not decrement twice
counter never becomes negative
concurrent reply creation versus parent deletion cannot create an invalid active result
~~~

Delete P1 and assert:

~~~text
P2/P3/P4 survive
structural FKs survive
P1 normal detail is 404
P2 parent reference is tombstoned
P4 quote reference is tombstoned
P1 content is absent from both references
targeting repost relation is removed
~~~

## 18.4 Effective time

Prove:

~~~text
short/reply/quote published_at = Post.created_at
PostArticle top-level published_at = PostArticle.published_at
profile cursor handles mixed Post kinds with no duplicate/skip
Following direct activity uses the same value
recommendation recent/trending/tie-break uses the same value
malformed PostArticle never falls back to Post.created_at
~~~

## 18.5 Engagement

On a reply Post:

~~~text
Like
load state for multiple users
Unlike
replay snapshot/behavior processing
verify PostReaction, count, Redis, version, and recovery consistency
~~~

Repost the reply twice and prove one relation; undo and prove zero.

## 18.6 Reply signal

For P3 replying to P2 in conversation P1:

~~~text
P2.reply_count increments
PostBehavior reply signal is keyed to P1
UserPostRecoState reply signal is keyed to P1
actor profile invalidation occurs
no reply signal is written for P2 or P3
~~~

## 18.7 Following

Given B follows A:

~~~text
A root appears as post activity
A reply does not appear as direct activity
A quote appears as post activity
A repost of C content appears with actor=A and post.author=C
A repost of a reply may appear as repost activity
latest-per-Post dedupe and cursor order match section 11
fallback to eligible direct activity remains correct
~~~

## 18.8 Notifications

Assert:

~~~text
B Likes A.P1 -> A receives post_liked for P1
B replies P2 to A.P1 -> A receives post_replied for P2
C replies P3 to B.P2 -> B receives post_replied for P3
conversation-root author A does not receive the nested reply unless A is direct parent author
self-like/self-reply suppressed
event replay does not duplicate
deleted notification Post is filtered
API exposes post_id and conversation_id only
~~~

## 18.9 Events

Contract-test every final event:

~~~text
event type
schema version
aggregate type and ID
partition key
payload post_id fields
topic mapping
producer/consumer acceptance
invalid old article_id payload rejection
~~~

Recommendation payload tests must require schema version 3.

## 18.10 Recommendation regression

Prove:

~~~text
short root eligible
PostArticle eligible
quote eligible
reply excluded
deleted excluded
expired/future/malformed PostArticle excluded
PostEmbedding/PostBehavior/UserPostRecoState use post_id
tracking claims and trace/metric rows use post_id
equivalent Article-era fixture converted to Post retains numeric scores/order
reply signal maps to conversation root
~~~

Do not change expected ranking weights or performance thresholds.

## 18.11 Cache

Prove:

~~~text
post:detail:v1 namespace used
old Article cache is ignored
viewer state absent from shared cache
references hydrated after base cache
deleting a quoted or parent target immediately yields tombstone reference
deleted target content cannot survive through cache
~~~

## 18.12 Frontend

Update/add tests for:

~~~text
postToFeedPost as the sole mapper
PostCard short/PostArticle/quote/deleted-reference variants
PostDetail for root/reply/quote/PostArticle
flat direct reply flow
ArticleCreate Post payload
Like/Repost stale hydration and cross-surface sync keyed by postId
Profile/History/Following pages
notification navigation
recommendation attribution and telemetry post_id
route-entry/logout/user-switch lifecycle behavior
~~~

Required identity assertion:

~~~text
the same Post ID is used across feed, detail, profile, history, recommendation, notification, Like, and Repost
~~~

---

# 19. Final acceptance scenario

Execute:

~~~text
User A creates root P1
User B replies P2 to P1
User A replies P3 to P2
User B Likes P2
User B reposts P1 twice
there is one PostRepost
User B undoes repost
User B creates quote P4 to P1
User A deletes P1
~~~

Final requirements:

~~~text
P1 deleted
P2/P3/P4 active
P2 direct parent tombstoned, conversation_id=P1
P3 direct parent=P2, conversation_id=P1
P4 quote target tombstoned
P1 content not exposed
P2 Like state internally consistent
no active B->P1 repost
reply behavior for P2 is keyed to P1
all operational content identity uses Post ID
all active Post kinds have a frontend detail surface
no Article/Comment translation layer exists
~~~

Failure is release blocking.

---

# 20. Validation commands

The current CI workflow is authoritative.

Backend, from D:\code\mf\Go.exchange:

~~~powershell
gofmt -w <only changed Go files>
go vet ./...
go test ./... -count=1
~~~

Run PostgreSQL integration tests with the repository's POSTGRES_TEST_DSN workflow against an empty test database. A skipped database suite is not a pass.

Frontend, from D:\code\mf\Exchangeapp_frontend on Windows:

~~~powershell
npm.cmd test
npm.cmd run build
~~~

On CI/Linux use the package-script equivalents:

~~~text
npm test
npm run build
~~~

Run the current recommendation performance scenarios and container builds required by CI.

Do not:

~~~text
delete tests to make the suite green
skip failing tests
lower performance thresholds
disable runtime schema validation
disable recommendation assertions
claim PostgreSQL, Redis, Kafka, browser, Docker, or deployment acceptance when not run
~~~

---

# 21. Completion grep and semantic audit

Search executable backend and frontend code for:

~~~text
ArticleID
article_id
CommentID
comment_id
models.Article
models.Comment
ArticleReaction
ArticleRepost
ArticleEmbedding
ArticleBehavior
UserArticleRecoState

/api/articles
/api/comments
/api/users/:id/articles
/api/recommendations/articles
/api/article-view-events

articleToFeedPost
recommendationToFeedPost
followingTimelineItemToFeedPost

article:detail
article:like
article:likes

article.viewed
article.liked
article.unliked
article.like.snapshot
article.embedding.requested
article.reaction.applied
comment.created
~~~

Do not require the word Article to disappear globally.

Allowed examples:

~~~text
PostArticle
ArticleCreateView
article draft/editor state
article title/preview/cover/expiry
article-cover upload
historical documentation explicitly marked superseded
~~~

Every remaining Article or Comment hit that might represent generic content identity must be listed and justified in the final report.

---

# 22. Definition of Done

Complete only when:

~~~text
Post is the sole content root and ID
reply and reply-to-reply are Posts
quote is a Post
long article is Post plus PostArticle
pure repost is PostRepost
Post kind combinations are validated
effective_published_at is shared across all time-sensitive surfaces
deletion preserves descendants and quote graph without leaking content
Like/Repost reliability semantics remain intact
reply recommendation signal maps to conversation root
nested reply notification targets direct parent author
Following preserves its cursor/dedupe/fallback contract
recommendation identity changes without scoring redesign
event schemas/topics are atomically Post-based
notification stores and returns Post identity only
runtime schema version 2 passes on an empty database
frontend has one Post API type and one canonical mapper
every active Post kind has a generic detail route
legacy Article/Comment operational identity is gone
all available CI gates pass
unavailable runtime gates are truthfully reported
~~~

---

# 23. Final agent report

Report:

~~~text
1. files changed
2. schema/tables/constraints/indexes
3. Post kind and transaction implementation
4. API and exact JSON changes
5. cache and Redis namespace changes
6. event types, schema versions, topics, and payloads
7. notification recipient/navigation behavior
8. recommendation identity/time/signal changes
9. frontend route/type/mapper changes
10. tests added or rewritten
11. commands run and exact results
12. PostgreSQL/Redis/Kafka/Docker/browser/deployment checks that were not run
13. retained Article occurrences and justification
14. remaining technical debt
~~~

Do not declare completion based only on compilation or mock-only tests.
